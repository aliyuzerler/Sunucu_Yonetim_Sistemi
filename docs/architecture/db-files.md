# Veritabanı Yönetimi ve Scoped Dosya Yöneticisi (Faz 10)

Bu belge Faz 10 teslimatının yerleşimini, işlem tablosunu, güvenlik modelini ve dürüst destek sınırlarını kaydeder. Karar gerekçeleri **ADR-0014**'tedir.

## Yerleşim

```
Control Plane (modüler monolith)
  tasks/ops_faz10.go      → 16 op spec (11 db + 5 fs), strict payload validator'lar
  dbcred/                  → şifre seal (AES-256-GCM) + tek-seferlik reveal + scrubber
  agentsrv/server.go       → ResultScrubber: persist ÖNCESİ şifre ayrıştırma
  httpapi                  → GET /nodes/{id}/db-credentials (db:read)
                             POST /db-credentials/{id}/reveal (db:control, tek seferlik)
  db/migrations/0014       → db_credentials + yalnız yeni izinler (CI-04 dersi)

Agent
  internal/dbops           → PostgreSQL/MariaDB-MySQL adaptörü (yalnız stdlib)
  internal/fsops           → scoped dosya yöneticisi (openat+O_NOFOLLOW, /proc kanıtı)
  internal/broker          → allowlist + strict şema; --db-enable / --fs-enable kapıları
```

## İşlem tablosu

| Op | Sınıf | İzin | Not |
|---|---|---|---|
| `db.instance.list` | read_only | db:read | soket taraması + sürüm sorgusu; tespit, mutasyon yok |
| `db.database.list` | read_only | db:read | ad + sunucu-üretimi boyut |
| `db.database.create` | idempotent | db:control | mevcutsa no-op; korumalı adlar tipli red |
| `db.database.delete` | destructive | db:control | retry-güvensiz; korumalı adlar tipli red |
| `db.user.list` | read_only | db:read | **şifre materyali TAŞIMAZ** (yalnız ad/host) |
| `db.user.create` | idempotent | db:control | şifre node'da üretilir, sonuçta TEK SEFERLİK; mevcut kullanıcı → no-op (şifre dönmez) |
| `db.user.delete` | destructive | db:control | kimlik satırları düşürülür |
| `db.grant.set` | idempotent | db:control | desired-state: önce REVOKE sonra seviye |
| `db.backup` | non_idempotent (retry-safe) | db:control | marker + sha256 + bytes; ≥64 MiB boş yer; ≤200 dosya |
| `db.backup.list` | read_only | db:read | yalnız marker'lı dosyalar listelenir |
| `db.restore` | destructive | db:control | yalnız marker'lı yönetilen dosya (`ErrForeignBackup`) |
| `fs.list` | read_only | files:read | sınırlı Readdirnames; symlink girdileri görüntü amaçlı |
| `fs.download` | read_only | files:read | ≤512 KiB; sha256 + MIME |
| `fs.upload` | idempotent | files:control | ≤512 KiB; uzantı+magic-byte+MIME politikası; O_EXCL / temp+rename |
| `fs.chmod` | idempotent | files:control | ≤ 0777; setuid/setgid/sticky yasak; fd-üzerinden |
| `fs.chown` | idempotent | files:control | hedef payload'da YOK — site kullanıcısı türetilir |

Registry toplamı: 51 → **67 operasyon**. Capability sayısı (unix): 35 → **51**.

## Panel-DB ayrımı (kriter 1) — yapısal kanıt

- `db.*` payload şemalarında host/port/DSN alanı yoktur; kontrol düzlemi validator'ı bilinmeyen alanı reddeder.
- dbops yalnız node-local unix soketlerine bağlanır (`/run/postgresql/.s.PGSQL.5432`, `/run/mysqld/mysqld.sock`); PostgreSQL erişimi `runuser -u postgres -- psql -h /run/postgresql` (peer auth) ile yapılır.
- Korumalı veritabanları: `postgres, template0, template1, information_schema, performance_schema, sys, mysql, server_control, serverctl, srvctl` + `srvctl_`/`serverctl_` önekleri. Korumalı kullanıcılar: `root, postgres, mysql, admin, debian-sys-maint, mysql.*, serverctl, server_control, srvctl`. Red broker + runner katmanlarında (derinlemesine savunma).
- Panelin kendi PostgreSQL kimlik bilgileri hiçbir task payload'ına girmez.

## Şifre yaşam döngüsü (kriter 2)

```
node (dbops): crypto/rand 32 alnum → CREATE USER ... IDENTIFIED BY '<pw>'
   ↓ task result: { credentials: { password, fingerprint } }
agentsrv ResultScrubber (persist ÖNCESİ):
   password → AES-256-GCM seal (CP_SECRETS_KEY) → db_credentials.password_encrypted
   result.credentials ← { credential_id, fingerprint, one_time_reveal }  (şifre SİLİNİR)
REST: GET /nodes/{id}/db-credentials  (db:read — asla blob)
      POST /db-credentials/{id}/reveal (db:control — TEK SEFERLİK, audit'li; 2. çağrı 409)
```

- Anahtar PostgreSQL'de yaşamaz (Faz 09 `dns_providers` disiplini).
- CP_SECRETS_KEY ayarsızsa: şifre redakte edilir, hiçbir yerde saklanmaz, UI dürüst gerekçeyi gösterir (redaksiyon retention'ı yener — fail-closed).
- tasks.result / audit / outbox / log'ların hiçbirinde düz metin şifre yoktur; scrub birim + PG testleriyle doğrulanır.

## Backup/restore disiplini (kriter 3)

- Yedekler `/var/backups/srvctl/db/<kind>/<db>-<UTCts>.sql` altında 0600 izinle yazılır; ilk satır marker: `-- server-control managed db backup (db.backup)`.
- PostgreSQL: `pg_dump --format=plain --no-owner --no-privileges` (tek MVCC snapshot; yazıcılar çalışırken tutarlı).
- MariaDB/MySQL: `mysqldump --single-transaction --quick --no-tablespaces` (InnoDB tutarlı snapshot).
- Restore: dosya adı allowlist'i + dizin hapsi + marker doğrulaması; sonra `psql -d <db> -f` / `mysql <db> < stdin`.
- Yarım restore riski belgelenmiştir: hedef veritabanı kısmi veri içerebilir; operatör yeniden restore ile yakınsar (dump'ın kendi tutarlılığı korunur).

## Dosya yöneticisi güvenlik modeli (kriter 4+5)

- **Kök türetme:** `root` payload'u yalnız site adıdır (webops allowlist); docroot = `webops.DocRoot(webRoot, site)` = `/var/www/server-control/<site>/html`. Raw filesystem yolu hiçbir yerde kabul edilmez → host geneli browse yapısal olarak imkânsız.
- **Yarışsız dolaşım:** `openat` zinciri (ara bileşenler `O_RDONLY|O_DIRECTORY|O_NOFOLLOW`); final bileşen her işlemde `O_NOFOLLOW` ile açılır.
- **Kapsam kanıtı:** açılan fd'nin çekirdek-gerçek yolu `/proc/self/fd/<n>` okunur; kanonik docroot içinde değilse `ErrScope`. Site kullanıcısının yürütme sırasındaki swap/dizin-adi değişiklikleri (TOCTOU) bu kanıtı geçemez.
- **Symlink politikası:** symlink'ler listelenir (tür + hedef görüntüsü) ama download/upload/chmod/chown'a konu olamaz (`ErrSymlink`); overwrite yolu mevcut symlink'i değiştirmez.
- **Boyut:** indirme ve yükleme 512 KiB raw tavan (`ErrTooLarge`); base64 payload ≤ 768 KiB; broker istek satırı 1 MiB.
- **MIME/uzantı:** uzantı denylist (.php ailesi, .cgi/.pl/.py/.sh, .service, .so, .ht*, .key/.pem/.crt/…) + magic-byte denetimi (`#!`, ELF, MZ, class) + `DetectContentType` denylist'i — defense in depth; asıl sınır kök hapsi + O_NOFOLLOW'dur.
- **İzinler:** chmod oktal ≤ 0777 (setuid/setgid/sticky tipli red); chown hedefi payload'da yoktur — `webops.SiteUser(site)` türetilir, yoksa `ErrNoSiteUser`.

## Dürüst destek sınırları (bu fazda bilinçli olarak yapılmayanlar)

- Uzak (TCP) DB instance yönetimi, replication/PITR, tablo düzeyi işlemler — sonraki fazlar.
- Windows dosya yöneticisi — bu fazda Linux docroot'ları (web hosting ADR-0012 Linux'a bağlı).
- 512 KiB üstü dosya aktarımı — chunking gerekiyorsa ayrı ADR.
- DB şifre rotasyon op'u (db.user.rotate) — şimdilik kullanıcıyı yeniden oluşturmak yeterli ve dürüst.
- Backup arşivleme/uzak kopyalama — yedekler node-local; taşıma sonraki fazlar.
- **MySQL/MariaDB istemci auth varsayımı:** `mysql`/`mysqldump` root@localhost'un `unix_socket` eklentisiyle bağlandığını varsayar (Debian/Ubuntu/MariaDB paket varsayılanı). Root@localhost parola-auth'lu hostlarda db ailesi dürüst executor hatası döndürür; istenirse `--defaults-extra-file` (0600 temp) desteği ayrı değişiklik olarak eklenir.

## Yapılandırma

| Bayrak/Env | Anlamı |
|---|---|
| `server-privd --db-enable` | db ailesini aç (VARSAYILAN KAPALI) |
| `server-privd --fs-enable` | fs ailesini aç (VARSAYILAN KAPALI) |
| `CP_SECRETS_KEY` | db_credentials seal anahtarı (base64 32 bayt); ayarsızsa şifreler redakte edilir |

## Testler

- `internal/dbops`: birim (SQL üretimi, korumalı adlar, marker mekanikleri, sınırlar) + `EXPECTED_DB=1` gerçek lifecycle (CI `agent-dbfs`).
- `internal/fsops`: güvenlik negatif testleri (traversal, symlink escape — dosya+dizin, iç symlink, boyut, MIME, mod bitleri, geçersiz kök) + `EXPECTED_FS=1` gerçek lifecycle.
- `internal/dbcred`: scrub birim testleri (her başarısızlık yolunda redaksiyon kazanır) + PG yaşam döngüsü (TEST_DATABASE_URL).
- `internal/broker`: db/fs aile şema negatifleri + kapı-off tipli redleri + runner hata eşlemesi.
- `internal/httpapi`: rota-regresyon (Faz 09 route kayıt düzeltmesi dahil) + db-credentials auth kapıları.
- E2E: `db-files.spec.ts` (3 test, mock) + 4 ekran görüntüsü `docs/reviews/screenshots/phase-10/`.
