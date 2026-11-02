# Backup Architecture (Faz 14)

Kapsam: kontrol paneli yedeği, node veri yedeği, doğrulama durumu ve izole restore tatbikatları. ADR-0018 ile bağlıdır.

## Kapsam ayrımı (kriter 1)

| | Control Plane yedeği | Node veri yedeği |
|---|---|---|
| Motor | CP worker'ında COPY tabanlı mantıksal döküm (stdlib + pgx) | Node'da restic (broker ailesi `--backup-enable`, VARSAYILAN KAPALI) |
| Kayıt | `backups.scope = 'control_plane'` (`node_id` YASAK) | `backups.scope = 'node'` (`node_id` ZORUNLU — CHECK) |
| Tetik | `backup.cp.run` görevi (control_plane hedefli) | `backup.run` görevi (node hedefli) |
| Depo | `CP_BACKUP_DIR` (varsayılan `/var/backups/server-control/control-plane`, 0700) | Node'da `/var/backups/server-control/restic` (node-managed sabit) |
| Gizli | CP_SECRETS_KEY depoya ASLA yazılmaz (kriter 3) | restic parolası `/etc/server-control/backup/restic-password` (0600; repo kökünden AYRI) |

`backups` kaydı doğrulama durumunu taşır: `verify_state ∈ {unverified, verified, failed}`, `verify_at`, `verify_detail`, `sha256`, `size_bytes`, `snapshot_id` (node), `storage_path` (CP).

## CP yedeği biçimi (SCP-BACKUP-V1)

```
SCP-BACKUP-V1
format=copy
created_at=<RFC3339>
tables=<n>

TABLE "public"."users" rows=0000000000000002
<gerçek COPY text satırları, tam 2 satır>
ENDTABLE
...
FOOTER tables=<n> rows=<n> sha256=<hex; tüm footer-öncesi baytlar>
```

- Tutarlılık: TÜM tablolar tek `REPEATABLE READ` işleminde `COPY ... TO STDOUT` ile akıtılır (veritabanı-yerel tek anlık görüntü; canlı dosya kopyası DEĞİLDİR).
- Satır sayıları sabit-genişlikli alanlara yazılır ve COPY sonrası yerinde yamalanır; footer sha256 son dosya üzerinden hesaplanır.
- Tablo sırası FK grafiğinin topolojik sırasıdır (Kahn; catalog kaynaklı) — restore kısıt ihlali yaşamaz.
- Boyut tavanı 512 MiB (tipli `ErrBackupLimit`; CP veritabanı operasyonel meta veridir).

## Doğrulama (kriter 2) — "başarılı yedek ≠ geri yüklenebilir"

1. `Dump` biter bitmez artifact diskten yeniden okunur: marker, başlık, bölüm yapısı, satır sayıları ve footer sha256 doğrulanır → `verify_state=verified` + `verify_at` kaydedilir. Doğrulama başarısızsa yedek `failed` olur (sessiz kabul yok).
2. `backup.cp.verify` görevi her zaman yeniden doğrulama yapabilir (bozulan dosya `verify_state=failed` + `backup_corrupt`).
3. Node: `backup.run` inline `restic check` (metadata) → verified; `backup.verify` → `restic check --read-data-subset=10%` (gerçek veri örnekleme). Node raporu CP kaydına aynen taşınır; node doğrulamadıysa kayıt asla verified görünmez.
4. Anahtar sızma taraması: her doğrulama pass'i artifact'i CP_SECRETS_KEY değeri için tarar; bulursa `ErrKeyMaterialInBackup` — doğrulama BAŞARISIZ (kriter 3, fail-closed).

## İzole restore tatbikatı (kriter 2)

CP (`backup.cp.restore_drill`):
1. Hedef adı sunucu ÜRETİR: `scp_drill_<8hex>` (allowlist `^scp_drill_[0-9a-f]{8}$`; canlı DB adı tipli `ErrUnsafeTarget` ile reddedilir).
2. `CREATE DATABASE` (CREATEDB yetkisi yoksa tipli `ErrDatabaseDenied` — dürüst engel).
3. Şema: gömülü migration'lar (`db/migrations`) hedef DB'de oynatılır — el yapımı DDL kopyası YOK.
4. Veri: artifact bölümleri `COPY ... FROM STDIN` ile topo sırada akıtılır (önce seed temizliği için tüm public tablolar TRUNCATE).
5. Sequence'ler `setval(max(id))` ile ileri alınır.
6. Doğrulama: tablo başına satır sayıları artifact ile karşılaştırılır; her eşleşme `tables_restored/rows_restored` olarak kayda geçer.
7. Temizlik: bağlantılar sonlandırılır + `DROP DATABASE` (best-effort; başarısızsa tatbikat detayında dürüst uyarı).

Node (`backup.restore`): hedef yalnız `/var/backups/server-control/restore/rest-<6hex>` — YENİ dizin (varsa tipli `ErrTargetExists`), payload'da hedef alanı YOK, restore başarısızsa dizin silinir. Sonuç: hedef + dosya/bayt sayıları (bilinmiyorsa 0 — uydurma yok).

Tatbikat kayıtları `restore_drills` tablosunda; UI Yedekler sayfasında listelenir.

## Node restic işlem ailesi (broker `--backup-enable`)

| Op | Sınıf | Davranış |
|---|---|---|
| `backup.repo.init` | idempotent | Repo yoksa kur (restic init); varsa `already=true` (dürüst). |
| `backup.run` | non-idempotent | Sabit argv `restic --repo ... --password-file ... backup --json` + inline check; yollar ≤8, mutlak, yapılandırılmış `--backup-source` köklerinin ALTINDA olmalı (payload yüzeyi genişletemez). |
| `backup.snapshot.list` | read-only | `snapshots --json` (limit ≤100). |
| `backup.verify` | read-only | `check --read-data-subset=10%`. |
| `backup.restore` | non-idempotent | Snapshot → izole dizin (yukarıda). |

Parola: RESTIC_PASSWORD_FILE (env; asla argv/sonuç). `ValidateKeySeparation` her işleyişte: parola dosyası repo içinde → tipli `ErrKeySeparation`.

## Görev → kayıt akışı

`backup.*` görevi SUCCEEDED olduğunda agentsrv `BackupSink` (backup.NodeSink) sonucu okur ve scope=node kayıtlarını/tatbikat satırlarını yazar. Node kimliği görevden alınır — sonuç alanlarına ASLA güvenilmez. Kayıt hatası görev sonucunu bozmaz (dürüst uyarı günlüğü).

## RBAC + audit

- `backup:read` → GET /backups, /restore-drills, backup.snapshot.list
- `backup:control` → run/verify/drill + node backup.* kontrol işlemleri
- Audit: görev akışı (task.create/complete/fail) + operator eylemleri görev motoru üzerinden denetlenir.

## Dürüst sınırlar (bu fazda YOK)

- Canlı veriye in-place restore; otomatik zamanlanmış yedek (schedule Faz 15+ konusu); restic forget/prune/rotasyon; şifreli repo (restic password zaten repo şifrelemesini sağlar — dosya şifreleme katmanı ayrı değildir); S3-uyumlu uzak repo bağlama (bulut sağlayıcı bağdaştırıcısı bu fazda yalnız envanter/silme yüzeyi verir; restic remote backend entegrasyonu ayrı karar konusudur).
