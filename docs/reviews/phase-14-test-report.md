# Faz 14 Bağımsız Test Raporu — Yedekleme, geri yükleme, bulut ve çoklu sunucu ölçekleme

- **Tarih:** 2026-11-03
- **Test eden:** ZCode (bağımsız test rolü — `prompts/phase-test/phase-14-test.md`)
- **İncelenen aralık:** 126bc1d (Faz 14 implementasyonu, işlenmemiş push) + bu oturumda düzeltilen test bulgusu
- **Başlangıç noktası:** b5c9199 (Docs: Faz 13 kapanışı) → tag v0.14.0 = febbd52, CI 15/15 yeşil (run 41498517797)
- **Karar:** **ACCEPTED — READY** (5/5 zorunlu kriter PASS; 1 test bulgusu düzeltildi, kritik/yüksek açık bulgu yok)

## Test ortamı (bu oturum, dürüst kayıt)

Sandbox taze başladığı için araç zinciri yeniden kuruldu: Go 1.27.3 (/tmp/gotc),
PostgreSQL 16.1 portable (önceki oturum kalıntısı pgdata + `server_control_test`
veritabanı bu oturumda oluşturuldu — CI'ın service container karşılığı), restic
0.18.0 resmi release ikilisi (CI'da apt restic). Playwright chromium 1.57.0
yerel cache'ten. `TEST_DATABASE_URL` ile CP entegrasyon testleri **yerelde
gerçek PostgreSQL üzerinde** koştu (Faz 13 raporundaki "sandbox'ta koşmaz"
kısıtı bu oturumda kalktı).

## Zorunlu kabul kontrolleri

### 1) Control Plane backup ve node data backup ayrılmalı — **PASS**

- **Yapısal ayrım (DB seviyesi):** `db/migrations/0018_backup_cloud.up.sql:25-44`
  — `backups.scope ∈ {control_plane, node}` (CHECK) ve `:43`
  `backups_node_scope CHECK ((scope='node') = (node_id IS NOT NULL))`: node
  yedeği node_id'siz, CP yedeği node_id ile **yazılamaz**.
- **Kod seviyesi:** `internal/backup/backup.go:26-29` `ScopeControlPlane` /
  `ScopeNode` sabitleri; `internal/backup/restore.go:32-34` — drill yalnız
  `ScopeControlPlane` kayıtta çalışır (`ErrInvalidState` aksi halde).
- **Sink tarafı:** `internal/backup/service.go:126-131` — `NodeSink.RecordNodeTask`
  node kimliğini **görevden alır**, sonuç alanlarına güvenmez ("the sink NEVER
  trusts result fields for scoping"); `:186-188/:206-208` — verify/restore
  sonuçlarında `rec.NodeID != nodeID` → tipli red ("başka node'a ait").
- **Operasyon ayrımı:** `internal/tasks/ops_faz14.go:228-330` — node işlemleri
  `backup.*` (repo.init/run/snapshot.list/verify/restore), CP işlemleri
  `backup.cp.*` (run/verify/restore_drill) ayrı ad uzayında; yorum
  `:18-19` sözleşmeyi yazar.

### 2) Backup integrity/verification state raporlanmalı; restore isolated target üzerinde denenmeli — **PASS**

- **CP dump doğrulamasız success olmaz:** `internal/backup/dump.go:260-266`
  — dump sonrası `verifyFile` ZORUNLU; doğrulama hatası → `StatusFailed`
  kaydı. `verify_state ∈ {unverified, verified, failed}` (`backup.go:34-42`)
  + `verify_at`/`verify_detail` kolonları (migration `:31-34`).
- **Artifact bütünlüğü:** `dump.go:414-505` `verifyStream` — marker/header/
  tablo bölümleri/satır sayıları/`FOOTER sha256` (footer öncesi tüm baytların
  özeti) uçtan uca doğrulanır; bozuk dosya `ErrCorruptBackup`. Sabit-genişlikli
  satır sayacı `:46` (16 hane, yerinde patch) format şeması `:402-413`.
- **İzole restore tatbikatı (CP):** `internal/backup/restore.go:24-71` —
  hedef adı **üretici tarafında** `scp_drill_+8hex` olarak üretilir ve
  `validateDrillDBName` allowlist'inden geçer (`backup.go:172-191`); canlı DB
  adı tipli red. Tatbikat: CREATE DATABASE → gömülü migration şema → TRUNCATE
  seed → COPY replay → tablo başına satır karşılaştırması (`:129-141`) →
  DROP (`:351-363`, bağlantı sonlandırma ile, dürüst temizlik uyarısı).
  Gerçek PG üzerinde `TestControlPlaneBackupVerifyAndDrillPG` **PASS**.
- **Node tarafı izolasyon:** `agent/internal/backupops/runner_linux.go:355-391`
  — restore hedefi payload'dan GELEMEZ (`RestorePayload`'da target alanı YOK,
  `backupops.go:92-97`); node `rest-<6hex>` dizinini yönetilen kök altında
  üretir. Başarısız restore dizini temizler (`:381`).
- **Node doğrulaması dürüst:** `runner_linux.go:270-281` — backup sonrası
  `restic check` zorunlu; `:338-353` `backup.verify` =
  `check --read-data-subset=10%` (gerçek kısmi veri okuma); durum
  `verified/failed/unverified` sonuç şemasında (`:53-61`), sink
  `service.go:163-173` eksik/başarısız node check'i asla verified göstermez.
- Kanıt: `TestControlPlaneBackupVerifyAndDrillPG`, `TestVerifyStreamValid`,
  `TestVerifyStreamRejectsCorruption`, `TestVerifyStreamFooterShaMismatch`,
  **`TestRealResticLifecycle` (gerçek restic 0.18.0: init → backup →
  snapshot list → verify → İZOLE restore + içerik kanıtı) PASS**.

### 3) Encryption master key backup repository ile aynı yerde tutulmamalı — **PASS**

- **CP:** `internal/backup/backup.go:101-130` `ValidateKeySeparation` —
  anahtar dosyası (veya symlink'li atası) BackupDir içinde ise
  `ErrKeyInsideRepo` (resolveExisting ile symlink kaçışı kapatılır `:132-154`).
  `CP_SECRETS_KEY` ortamdan gelir, diske yazılmaz (`service.go:24-29`).
- **Sızma taraması (fail-closed):** `dump.go:388-397` — her doğrulamada
  artifact, anahtar malzemesi için ikinci geçişte taranır; bulursa
  `ErrKeyMaterialInBackup` → yedek FAILED. Kanıt:
  `TestDumpKeyMaterialLeakDetectedPG` (anahtar sızdırılmış artifact reddi,
  gerçek PG) + `TestContainsPatternBoundary` (sınır bölünmeli eşleşme).
- **Node:** `agent/internal/backupops/backupops.go:8-12` — restic deposu
  `/var/backups/server-control/restic`, parola dosyası
  `/etc/server-control/backup/restic-password` — **ayrı kökler**; parola
  dosyasının repo içi konumu her operasyon öncesi tipli red
  (`runner_linux.go:143-149`, `ValidateKeySeparation :152-174`,
  `ErrKeySeparation`). Parola argv'ye ASLA girmez: `--password-file` yolu +
  `RESTIC_PASSWORD_FILE` env (`:176-186`); sonuç şemalarında parola alanı yok.
- Repo içi parola reddi testleri: `backupops_test.go:255-267` (RepoInit/Backup
  ihlalde reddedilir) + `realhost_test` lifecycle'ında parola ayrı dizinde.

### 4) En az iki-üç node ile task isolation, offline node, reconnect, retry ve certificate revoke test edilmeli — **PASS**

- `backend/control-plane/internal/agentsrv/multinode_test.go` — gerçek
  PostgreSQL + gerçek TLS üzerinde 3 sanal node (enroll → stream → hello/ack):
  - `TestMultiNodeTaskIsolation` — sıkışan node diğerlerini bloklamaz;
    yanlış-node sonucu red.
  - `TestOfflineNodeQueueAndReconnect` — offline node kuyruğu + yeniden
    bağlanınca teslim.
  - `TestMultiNodeLeaseRetrySemantics` — lease/retry; tavan sonrası
    `lease_expired`.
  - `TestMultiNodeThroughput` — 3 node × 10 görev, attempts=1.
  - `TestCertRevocationBlocksReconnect` — revoke/retired sertifika ile
    yeniden bağlanma `Unauthenticated`.
- Hepsi **PASS** (-race, yerelde gerçek PG 16.1; CI'da PG 18).

### 5) Cloud provider adaptörleri ayrı scope/credential/permission matrix; kaynak silme onay gerektirmeli — **PASS**

- **Adapter sözleşmesi:** `internal/cloud/cloud.go:99-113` `Provider`
  arayüzü; `:59-62` `PermissionMatrix` — her sağlayıcı kendi matrisini
  beyan eder, UI'ya birebir servis edilir (`service.go:129-146`).
- **İlk adaptör S3-uyumlu:** `internal/cloud/s3.go:37` `PermissionMatrix()`
  (list/read/delete — delete `destructive`+`confirm_required`); AWS SigV4
  **yalnız stdlib** (`:111-121+`); transport politikası
  `validateEndpoint` (`:68-86`): https zorunlu, http yalnız döngü adresi.
- **Silme onayı SERVİS sınırında:** `internal/cloud/service.go:170-173`
  `DeleteResource` — `confirm=false` → `ErrConfirmationRequired` (UI gate'i
  tek savunma değildir). Dolu bucket → `ErrResourceNotEmpty` ("panel zorla
  silmez"); HTTP'siz geçersiz ad red. Kanıt: `TestS3DeleteResourceGates`,
  `TestServiceDeleteResourceConfirmGate` **PASS**.
- **Kimlik bilgisi disiplini:** kayıtta zorunlu alan doğrulaması + AES-256-GCM
  seal (`service.go:52-87`); okuma yolları ciphertext'i ASLA döndürmez
  (`store_pg.go:70-94` SELECT listesinde `credentials_encrypted` yok; yalnız
  `:123`'teki özel `EncryptedCredentials` açar). Anahtar yoksa tüm
  kimlikli işlemler `ErrKeyNotConfigured` (dürüst red).
- **RBAC bağlantısı:** `internal/rbac/rbac.go:103-106` yeni izinler;
  `httpapi/server.go:251-260` — listeler `*:read`, mutasyonlar `*:control`
  + CSRF; migration **yalnız yeni izinleri** seed eder (CI-04 dersi,
  `0018_backup_cloud.up.sql:78-91`).

## Bulunan hatalar ve önem dereceleri

| ID | Önem | Tür | Açıklama | Durum |
| --- | --- | --- | --- | --- |
| IT-01 | ORTA | TEST | `TestRealResticLifecycle` geri yüklenen dosyayı `<target>/citest-source/etc-backup.txt` bekliyordu; restic'in GERÇEK davranışı snapshot'taki mutlak yolu hedef altında yeniden oluşturmaktır (`<target>/tmp/.../citest-source/...` — restic 0.18.0 ile elle doğrulandı). Commit hiç CI'dan geçmediği için bu, ilk push'ta `agent-backup` job'ını kırmızı yapacaktı (CI-48 ile aynı sınıf: ürün doğru, test beklentisi yanlış) | **DÜZELTİLDİ** — beklenti `strings.TrimPrefix(src, "/")` ile restic düzenine göre yazıldı (`realhost_test.go:129-134`); gerçek restic ile lifecycle PASS |
| IT-02 | BİLGİ | Test altyapısı | Sandbox'ta PostgreSQL entegrasyon testleri için `server_control_test` DB'si + `serverctl_test` parolası bu oturumda kuruldu (CI service container karşılığı); ilk koşuda 6 PG testi ortam eksikliğinden FAIL gördü, ortam sonrası aynı testler yeşil — ürün hatası değil | ORTAM KURULDU |
| IT-03 | BİLGİ | KAPSAM | `proto/` ve `gen/` bu fazda değişmedi (`git diff origin/main..HEAD -- proto/ gen/` boş) → CI `proto` job'ı bu fazdan etkilenmez; son yeşil koşu v0.14.0 bazında geçerli | BELGELENDİ |
| IT-04 | DÜŞÜK | Operasyon notu | `restic check --read-data-subset=10%` ilk kurulumda büyük repo'da zaman alabilir; broker `--backup-enable` VARSAYILAN KAPALI ve op zaman aşımı 4 dk (yapılandırılabilir) — operatör dokümanında yer alıyor (ADR-0018 + docs/architecture/backup.md) | BELGELENDİ |

## Çalıştırılan komutlar ve kısa çıktılar

| Komut | Sonuç |
| --- | --- |
| `cd backend/control-plane && gofmt -l . && go vet ./... && go build ./...` | temiz |
| `cd backend/control-plane && go test -race -count=1 ./...` (TEST_DATABASE_URL ile gerçek PG) | **20/20 paket ok** (agentsrv 6.8s, backup, cloud, tasks, telemetry dahil) |
| `cd agent && go vet ./... && go build ./...` | temiz |
| `cd agent && EXPECTED_BACKUP=1 CGO_ENABLED=1 go test -race -count=1 ./...` | **18/18 paket ok** (backupops: mock süit + GERÇEK restic 0.18.0 lifecycle) |
| `GOOS=windows go vet ./... && GOOS=windows go build ./...` (agent) | temiz |
| `/tmp/backupops.test -test.run TestRealResticLifecycle -test.v` | **PASS** (init→backup→list→verify→izole restore+içerik) |
| `cd web && npm run lint && npm run typecheck` | temiz |
| `cd web && npm test` | **50/50** (11 dosya) |
| `cd web && npm run build` | temiz (bilinen chunk uyarısı) |
| `cd web && npx playwright test` | **43/43** (39 önceki + 4 yeni Faz 14); Faz 14 ekran görüntüleri tazelendi (4 PNG) |
| `bash scripts/secret-scan.sh` | temiz ✓ |
| Çoklu-node kanıtları (verbose) | 5 test PASS: isolation / offline+reconnect / lease-retry / throughput / cert revoke |

**NOT RUN / BLOCKED (dürüst işaretli):** gerçek S3 sağlayıcısına karşı verify
(sandbox'ta gerçek bucket yok — `httptest` sahte S3 süiti + SigV4 şekil
testleri kapsamı sağlar; operatör doğrulaması CloudPage "Doğrula" akışı);
gerçek node üzerinde restic kurulum disiplini (deploy README + agent README);
proto job yerelde koşulmadı (araç zinciri gerektirir — içerik değişmedi,
IT-03).

## Regresyon

Önceki fazların tüm süitleri taze koşuldu: agent 18/18 -race (vmops/mailops/
dbops/fsops/logops dahil), GOOS=windows vet+build, backend 20/20 -race
(registry 105-op kurulumu, route regression, RBAC, audit, identity...),
vitest 50/50, Playwright 43/43. Kırılan mevcut test olmadı; tek düzeltme
IT-01 (yalnız test dosyası). Migration 0018 up/down çift yönlü incelendi;
`testdb.Reset` listesi `backups/restore_drills/cloud_providers` içerir
(testdb.go:74-97) — izolasyon tam.

## Güvenlik değerlendirmesi

- **Enjeksiyon yüzeyi kapalı:** drill DB adı üretici-taraflı + allowlist;
  artifact tablo adları tekrar şekil-doğrulamalı (`quoteQualifiedName`,
  `TestQuoteQualifiedNameRejectsInjection`); restic argv sabit şablon, kabuk yok
  (`//nolint:gosec // fixed binary + argv, no shell`), payload'da repo/parola/
  hedef yok; backup yolları mutlak + `..` + kontrol-karakter reddi ve broker
  kaynak allowlist'i (`pathsWithinSources`) — payload yüzeyi genişletemez.
- **Anahtar ayrımı iki katman:** konum reddi (symlink çözümlü) + artifact
  sızma taraması (fail-closed, gerçek-PG testli).
- **Silme onayı servis sınırında**; dolu kaynak zorla silinmez; silmeler
  auditli (HTTP katmanı).
- **Node sonuçları sahteye karşı savunmalı:** sink node kimliğini görevden
  alır, yabancı-node sonuç şeması `DisallowUnknownFields`, verify durumu
  dürüst eşlenir.
- Secret-scan temiz; yeni kodda literal kimlik bilgisi yok (sahte S3 süiti
  değerleri test runtime'ında).

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS (file:line kanıtlı; CP/node backup
ayrımı DB CHECK ile yapısal, verify disiplini gerçek PG + gerçek restic ile
kanıtlı, multi-node kanıtları gerçek PG+TLS üzerinde, cloud silme onayı
servis sınırında testli). Tek orta bulgu IT-01 test beklentisiydi ve push
öncesi düzeltildi; kritik/yüksek açık bulgu yok.

**Sonraki faza geçiş: READY.**
