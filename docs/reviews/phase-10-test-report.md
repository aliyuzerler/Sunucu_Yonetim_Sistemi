# Faz 10 Bağımsız Test Raporu — Veritabanları ve güvenli dosya yöneticisi

- **Test tarihi:** 2026-11-02
- **Denetlenen commit:** 39b5012 (Faz 10 implementasyonu; taban 31783de = Faz 09 ACCEPTED/READY, tag v0.10.0)
- **Test eden rolü:** bağımsız denetçi (uygulama ajanı varsayımları alınmadı; kanıt file:line + komut çıktısı istendi)
- **Yerel doğrulama (taze koşu, 39b5012):** backend `go vet ./...` temiz + `go test ./...` **16/16 paket ok** (kritik paketlerde `-race`: dbcred/tasks/httpapi/rbac ok); agent `go vet` + `go test ./...` **14/14 paket ok** (broker/dbops/fsops ayrıca `-race` ok) + `GOOS=windows go build ./...` ok; web `tsc --noEmit` temiz + `vitest` **30/30** + `npm run build` ok + **Playwright 26/26** (5 mock spec; Faz 10 dahil 3 yeni test).
- **CI:** yerel ortamda Docker/PostgreSQL/MariaDB olmadığından PG entegrasyonu (TEST_DATABASE_URL) ve gerçek-DB/FS lifecycle (EXPECTED_DB=1/EXPECTED_FS=1) yerelde **NOT RUN**'dır; bunlar CI'da koşar. **İlk push koşusu (run 41368133074, commit 00a032e) 4 job düşürdü ve 3 gerçek bulgu yakaladı** — aşağıdaki düzeltmeler yapıldı ve ikinci koşu yeniden doğrulanmıştır:
  - **CI-28 (gerçek-host kanıtı, ORTA):** PG restore `runuser -u postgres -- psql -f <path>` postgres OS kullanıcısının root-aait 0700/0600 yedeği okuyamaması nedeniyle exit 1 (TestRealDBLifecycle) — CI'nın varlık sebebi tam olarak bu sınıf hataları yakalamak. Düzeltme: dosya root açar, STDIN üzerinden psql'e akıtılır; izin gevşetilmez.
  - **CI-29 (DÜŞÜK):** `broker/dbfs_test.go` build-tag eksikliği → GOOS=windows vet `dbops.Instance` undefined. `//go:build unix` eklendi.
  - **CI-30 (ORTA, flake):** vitest DbFsPanel testi `db-credentials` kartına `waitFor` olmadan erişiyordu — 3 koşuda 1 flake (yerel ilk turda 29/30, CI'da FAIL). `waitFor` eklendi; 6/6 + 2 tam-süit koşusu stabil.
  - **Üçüncü koşu (run 41369557474):** Windows yeşile döndü (CI-31 düzeltmesi çalıştı); iki yeni gerçek bulgu: CI-33 (fs.list çift-fd-kapanışı — `-race` altında EBADF) ve CI-34 (MySQL REVOKE 1141 — desired-state ilk-uygulama hatası; ilk iki koşuda postgres aşaması erken düştüğü için yüzeye çıkmamıştı). Her ikisi düzeltildi ve hermetik testlerle güvenceye alındı.
  - **İkinci koşu (run 41369009753):** Backend+Frontend yeşile döndü; iki yeni gerçek bulgu yakalandı ve düzeltildi: CI-31 (Windows pipe deny close-race → close-grace) ve CI-32 (PG DROP ROLE bağımlılığı → standart DROP OWNED prosedürü — agent-dbfs gerçek koşusunun ürettiği ikinci değerli kanıt).
  - gofmt kapısı üç format-dışı dosyayı yakaladı (son düzenlemelerden sonra formatlanmamıştı) — düzeltildi, `gofmt -l` iki modülde de temiz.
  - Düzeltmeler sonrası yerel yeniden doğrulama: backend 16/16, agent 14/14 (GOOS=windows vet+build dahil), vitest 30/30 (×2), Playwright 26/26.
- **Kapsam:** yalnız Faz 10 (`prompts/phase-test/phase-10-test.md`); sonraki faz başlatılmadı.

## Zorunlu kabul kontrolleri

### Kriter 1 — Panelin kendi PostgreSQL'i ile yönetilen müşteri DB instance'ları ayrılmalı → **PASS**

| Kanıt | Konum |
|---|---|
| `db.*` payload şemalarında host/port/DSN alanı YOKTUR (yalnız `kind`/`name`/`host`* — *MySQL kullanıcı bileşeni 'u'@'h', veritabanı bağlantısı DEĞİL); strict decode bilinmeyen alanı reddeder | `backend/control-plane/internal/tasks/ops_faz10.go` (validateDBList/validateDBDatabase/validateDBUser/validateDBGrant) |
| Adaptör yalnız node-local unix soketlerine bağlanır: `/run/postgresql/.s.PGSQL.5432` (`runuser -u postgres -- psql -h /run/postgresql`, peer auth), `/run/mysqld/mysqld.sock` (root socket auth) | `agent/internal/dbops/runner_linux.go:32-60,112` |
| Korumalı adlar: sistem DB'leri + panel konvansiyonları (`server_control`, `srvctl*`, `serverctl*`) + süper-user hesapları; create/delete/backup/restore'da tipli red | `agent/internal/dbops/dbops.go` (`protectedDatabases`/`protectedUsers`/`IsProtectedDatabase`); broker'da derinlemesine savunma `agent/internal/broker/protocol.go:497,512` |
| Negatif kanıt: korumalı adlara create/delete/backup yerelde de tipli red; panel DB'sine dokunma yolu yok | `agent/internal/dbops/dbops_test.go:TestProtectedRefusals` (PASS) |
| Dürüst kalıntı risk belgeli: egzotik adla panel DB'sini managed node'a koymak operatör disiplinine kalır | ADR-0014 Consequences + `docs/architecture/db-files.md` |

### Kriter 2 — Database/user/grant typed, scoped, auditli; password tekrar düz metin gösterilmemeli → **PASS**

| Kanıt | Konum |
|---|---|
| 11 db op typed spec: sınıflar (read_only/idempotent/destructive), `RequiredPerm` (db:read/db:control), timeout/payload sınırları; registry 51→**67** doğrulaması | `internal/tasks/ops_faz10.go:370+`; `service_test.go:217` (`!= 67` PASS) |
| Şifre NODE'da üretilir (crypto/rand 32 alnum — tek-tırnak SQL literalinde güvenli charset), payload'a KONMAZ | `agent/internal/dbops/dbops.go:GeneratePassword`; `runner_linux.go:374` (UserCreate) |
| Persist-öncesi ayrıştırma: agentsrv `ResultScrubber` şifreyi AES-256-GCM seal edip `db_credentials`'a yazar, sonuçtan SİLİR; tasks.result/audit/outbox'ta asla düz metin | `internal/agentsrv/server.go:67-77` + `handleOperationResult` scrub çağrısı; `internal/dbcred/dbcred.go:196+` (ScrubNodeResult) |
| Redaksiyon retention'ı yener: anahtarsız / provenance-eksik / seal-hatası yollarının HEPSİNDE şifre sonuçtan silinir + dürüst gerekçe | `dbcred_test.go` (TestScrubUserCreateWithoutKeyRedacts / WithoutNodeRedacts / UpsertErrorRedacts — PASS) |
| Tek-seferlik reveal: `UPDATE ... WHERE id=$1 AND revealed_at IS NULL RETURNING` (race-free); 2. çağrı tipli 409; reveal audit'li; liste asla blob döndürmez | `dbcred/dbcred.go:107`; `internal/httpapi/handlers_dbcred.go:43`; PG test `dbcred_pg_test.go:TestCredentialLifecyclePG` (CI'da koşar) |
| RBAC: migration 0014 yalnız yeni izinleri seed eder (CI-04 dersi); operator salt-okuma; REST uçları requirePermission ile kapılı | `db/migrations/0014_db_files.up.sql`; `httpapi/server.go:195-196` |
| User/grant listeleri şifre materyali taşımaz (pg_roles.rolname / mysql.user.User+Host kolonları yalnız) | `dbops/dbops.go` ListSQL; `dbops_test.go:TestUsersListNoPasswordColumns` (PASS) |

### Kriter 3 — Backup/restore DB-native tutarlılık kurallarıyla → **PASS** (gerçek-DB kanıtı CI'da — bkz. bulgular)

| Kanıt | Konum |
|---|---|
| PostgreSQL: `pg_dump --format=plain --no-owner --no-privileges` — tek MVCC snapshot (yazıcılar çalışırken tutarlı); MySQL: `mysqldump --single-transaction --quick --no-tablespaces` — InnoDB tutarlı snapshot | `agent/internal/dbops/runner_linux.go:623` (streamDump) |
| Streaming yazım (bellek sınırlı), marker'lı ilk satır, sha256+bytes raporu, fdatasync, yarım dosya silinir | `runner_linux.go:518` (Backup), `dbops.go:72` (BackupMarker) |
| Dürüst sınırlar: statfs ≥64 MiB boş yer; ≤200 dosya/kind; aynı-saniye çakışma tipli red | `runner_linux.go:596` (checkCapacity); test `TestBackupFileLimit` (PASS) |
| Restore kapıları: isim-allowlist + dizin-hapsi + marker doğrulaması → `ErrForeignBackup`; hedef DB varlık kontrolü | `runner_linux.go:719` (Restore); test `TestBackupRestoreLifecycle` (markersız dosya reddi + yok-dosya ErrNotFound — PASS) |
| Yedek listesi yalnız marker'lı dosyaları gösterir (yabancı dosya görünmez) | `TestBackupsListHidesForeign` (PASS) |
| Gerçek kanıt: CI `agent-dbfs` job'ı gerçek PostgreSQL+MariaDB lifecycle (create→user+grant→backup→restore→delete + gerçek pg_dump içerik denetimi) | `agent/internal/dbops/realhost_test.go` (EXPECTED_DB=1; CI'da PASS beklenir) |

### Kriter 4 — File manager canonical path, symlink, traversal, race, size, MIME/uzantı kontrolleri → **PASS**

| Kanıt | Konum |
|---|---|
| Yol doğrulama: göreli, `.`/`..` segment red, ≤16 segment, segment RE, ≤400 bayt | `agent/internal/fsops/fsops.go:ValidRelPath`; test `fsops_test.go:TestValidRelPath` (12 negatif kalıp — PASS) |
| Traversal/symlink: openat zinciri her bileşende `O_NOFOLLOW`; ara symlink tipli `ErrSymlink` (ENOTDIR-ELOOP ayrımı Fstatat ile); final symlink hiçbir işlemde izlenmez; mutlak yol reddedilir | `runner_linux.go:87` (walk), `:118` (mapOpenErr); testler `TestSecurityNegatives` (dosya/dizin/iç symlink escape hepsi reddedildi — PASS) |
| Race (TOCTOU): açılan nesnenin çekirdek-gerçek yolu `/proc/self/fd/<n>` ile kanıtlanır; kanonik docroot dışına swap → `ErrScope`; upload temp+rename aynı parent içinde (renameat kapsam-kaçışına kapalı) + temp fd kapsam kanıtı + symlink hedefi asla değiştirilmez | `runner_linux.go:44-56` (scopeProof), `:365` (Upload) |
| Boyut: indirme/yükleme 512 KiB raw tavan, base64 ≤768 KiB, erken red (diske yazmadan) | `fsops.go:DecodeContent`; test `TestDecodeContent` (PASS) |
| MIME/uzantı: denylist (.php ailesi/.cgi/.pl/.py/.sh/.key/.pem/.ht*/.so/…) + magic-byte (`#!`, ELF, MZ, class) + DetectContentType denylist'i (shebang text/plain döndürdüğü için kendi magic denetimi eklendi) | `fsops.go:CheckUploadPolicy`; test `TestCheckUploadPolicy` (PASS) |
| İzinler: chmod oktal ≤0777, setuid/setgid/sticky red, fd-üzerinden Fchmod (yarışsız); chown hedefi payload'da YOK (site kullanıcısı türetilir; yoksa `ErrNoSiteUser`) | `runner_linux.go:486` (Chmod), `:541` (Chown); testler `TestParseMode`, `TestUploadChmodChownLifecycle`, `TestChownWithoutSiteUser` (PASS) |

### Kriter 5 — Dosya işlemleri yalnız atanmış site/root alanında; host geneli browse edilememeli → **PASS**

| Kanıt | Konum |
|---|---|
| Kök = site adı (webops site allowlist'i — tek doğruluk kaynağı); docroot yalnız addan türetilir (`webops.DocRoot` — webops/fsops paylaşımlı ihracım); raw path payload'a ASLA kabul edilmez | `fsops/fsops.go:118` (ValidRoot→webops.ValidSiteName); `webops/runner_linux.go:469` (DocRoot); `runner_linux.go:openRoot` |
| Olmayan/geçersiz kök tipli red: `ErrNotSiteRoot` (docroot yok) / `ErrInvalidRoot` | test `TestSecurityNegatives` (PASS) |
| Kapsam kanıtı her operasyonda (list/download/upload/chmod/chown) fd-düzeyinde | `runner_linux.go:scopeProof` çağrıları |
| Gerçek kanıt: CI `agent-dbfs` gerçek docroot üzerinde upload/download/chmod/chown + GERÇEK symlink-escape reddi (`/etc/passwd` linki `ErrSymlink`) | `fsops/realhost_test.go` (EXPECTED_FS=1; CI'da PASS beklenir) |

## Pozitif senaryolar (yerel, hermetik — PASS)

- DB: korumalı-ad redleri, geçerli SQL üretimi (PG/MySQL matrisi), şifre üretimi (charset/uzunluk/tekillik), yedek yazımı marker+sha256+bytes, marker'lı restore, yedek listesi, sınırlar.
- FS: liste (symlink girdisi + link hedefi görüntüsü, truncate dürüstlüğü), indirme (sha256+MIME), yükleme (create/overwrite, mod uygulaması, temp kalıntısızlık), chmod, chown (türetilmiş kullanıcı).
- Scrub: normal seal+ayrıştırma, idempotent replay (credentials bloğu yok → no-op), reveal akışı.

## Negatif senaryolar (PASS)

- Yetki: db/fs kapalı broker → tipli `unsupported_operation` (`TestDBFSFamiliesDisabledByDefault`); REST auth'suz 401 (`TestDBCredentialEndpointsAuthGated`).
- Bozuk payload: broker strict şema negatifleri — 27 negatif / 20 pozitif (`TestDBFSFamilyValidation`); CP mirror validator'ları (korumalı ad + RE + matris).
- Şifre gizliliği: scrub'un TÜM başarısızlık yollarında şifreyi kalıcı yüzeylerden sildiği birim testlerle kanıtlı.
- Symlink/traversal/boyut/MIME: `TestSecurityNegatives` + `TestCheckUploadPolicy` + `TestDecodeContent`.
- Tekrar/replay: db.user.create mevcut kullanıcı → no-op (şifre dönmez); reveal 2. çağrı → tipli 409 (PG testi).
- Offline node / unsupported capability: Faz 03'ten beri çerçeve davranışı (broker yok → executor relay hata; capability unavailable) — bu faz için değişmedi, regresyon 14/14 paket ok.

## Regresyon sonucu

- Backend: `go test ./...` 16/16 (Faz 02-09 testleri dahil; dbcred PG testleri CI'da). Agent: 14/14 + GOOS=windows build. Web: vitest 30/30 (Faz 08/09 testleri dahil), Playwright 26/26 (14 smoke/enrollment/telemetry/windows + 3 docker + 3 web-hosting + 3 dns-ssl + **3 db-files**).
- **Faz 09'dan taşın hata bu koşuda saptandı ve düzeltildi:** `/dns-providers` + `/certificates` route'ları Faz 09'da server.go'ya hiç kaydedilmemişti (yalnız import + alanlar; e2e gerçek modda skip olduğu için CI yakalamamıştı). Düzeltme `httpapi/server.go:181-196` (route kayıtları + constructor atamaları), regresyon `routes_regression_test.go` (rota varlığı + auth kapısı; PASS).

## Bulgular

| ID | Önem | Bulgu | Durum/Öneri |
|---|---|---|---|
| IT-01 | Orta | Gerçek-DB/FS lifecycle kanıtı (EXPECTED_DB=1/EXPECTED_FS=1) yerelde NOT RUN (sandbox'ta PostgreSQL/MariaDB yok) — kanıt CI `agent-dbfs` job'ına bağlı | Push sonrası CI doğrulanacak; gate'ler sessiz atlamayı yasaklar (CI-17). Kalıcı olarak sahibin disposable VM'de `deploy/README` prosedürini tekrarlaması önerilir |
| IT-02 | Düşük | Yarım restore operasyonel riski: hedef DB kısmi veri içerebilir; yeniden restore yakınsar | Kasıtlı tasarım — ADR-0014'te belgeli; kapanış gerektirmez |
| IT-03 | Düşük | `mysqldump`/`mysql` root socket auth varsayımı: Debian/Ubuntu/MariaDB `unix_socket` eklentisi varsayılır; root@localhost parola-auth'lu hostlarda db ailesi dürüst executor hatası verir | `docs/architecture/db-files.md` notuna eklendi (dürüst sınır); future: defaults-extra-file desteği |
| IT-04 | Bilgi | Dosya boyutu 512 KiB tavanı gerçek bir sınırdır; chunking gerekiyorsa ayrı ADR açılmalı (ADR-0014 Alternatifler'de erteleme kararı) | Açık iş takibi |
| IT-05 | Bilgi (pozitif) | Bağımsız koşu Faz 09'un eksik rota kaydını yakaladı ve düzeltildi (bkz. Regresyon) | Regresyon testiyle kalıcı güvence |
| IT-06 | Orta → **düzeltildi** | CI-28: PG restore izin hatası (postgres OS kullanıcısı root-un 0700 dizinindeki yedeği okuyamıyordu) — gerçek kanıt CI'nın değerini kanıtladı | STDIN akışı ile düzeltildi; izin gevşetilmedi |
| IT-07 | Düşük → **düzeltildi** | CI-29: dbfs_test.go unix build tag eksikliği (windows vet) | Tag eklendi |
| IT-08 | Orta → **düzeltildi** | CI-30: vitest reveal testi flaky (waitFor'sız async kart erişimi) | waitFor + 6/6 stabil + tam-süit ×2 |
| IT-09 | Düşük → **düzeltildi** | CI-31: Windows pipe deny-path'te writeResponse→Close yarışı (ERROR_NO_DATA; flake) | respond() 50ms close-grace; red yolları nadir |
| IT-10 | Orta → **düzeltildi** | CI-32: PG DROP ROLE, grant'lı rol için bağımlılık hatasıyla düşüyordu (grant→delete doğal akış bozulurdu) | PostgreSQL standart prosedürü: her DB'de DROP OWNED → DROP ROLE; sahiplik varsa tipli ErrConflict |
| IT-11 | Orta → **düzeltildi** | CI-33: fs.list çift-fd-kapanışı (fd-reuse → EBADF; CI `-race` altında patladı, yerelde sessizdi) | fd sahipliği tek yerde; `go test -count=3` + CI `-race` |
| IT-12 | Orta → **düzeltildi** | CI-34: MySQL REVOKE error 1141 — desired-state'in İLK uygulaması (fresh user) bozuluyordu | information_schema probe'u ile koşullu REVOKE + kaçışlı grantee literal'i + hermetik 2-alt-test |

## Test komutları ve kısa çıktılar (yerel, 39b5012)

```
backend/control-plane: go vet ./... → temiz
backend/control-plane: go test ./... → 16 paket ok
  (go test -race ./internal/dbcred ./internal/tasks ./internal/httpapi ./internal/rbac → ok ×4)
agent: go vet ./... → temiz; go build ./... → ok; GOOS=windows go build ./... → ok
agent: go test ./... → 14 paket ok
  (go test -race ./internal/broker ./internal/dbops ./internal/fsops → ok ×3)
web: npx tsc --noEmit → temiz; npx vitest run → 7 dosya / 30 test ok
web: npm run build → ok; npx playwright test → 26 passed
CI (push sonrası): agent-dbfs job — EXPECTED_DB=1 TestRealDBLifecycle|TestRealDBBackupContent,
  EXPECTED_FS=1 TestRealFSLifecycle; backend job — TEST_DATABASE_URL ile dbcred PG yaşam döngüsü
```

## Güvenlik değerlendirmesi

- Kriter 1 yapısal olarak çözülmüştür: panel DB'si hiçbir şemada temsil edilmez; dbops'un dış dünyaya tek çıkışı node-local soket + runuser/argv'dir; korumalı adlar iki katmanda red edilir.
- Şifre gizliliği storage-boundary'de (persist öncesi scrub) zorlanır — log/audit/outbox/result dörtlüsünün hiçbirine düz metin giremez; redaksiyon her başarısızlık yolunda kazanır (fail-closed) ve tek-seferlik reveal tek race-free statement'a bağlıdır.
- Dosya yöneticisinin güvenlik sınırı filtre değil türetme + çekirdek kanıtıdır: kök türetme (site adı → docroot) traversal'ı yapısal imkânsız kılar; openat+O_NOFOLLOW zinciri symlink'i her katmanda tipli reddeder; /proc fd kapsam kanıtı TOCTOU'yu kapatır. MIME/uzantı katmanı defense-in-depth'tir (dürüstçe öyle belgelenmiştir).
- Restore keyfi dosya kabul etmez (marker + isim allowlist + dizin hapsi) — bu, "privileged broker'da arbitrary file read/write" sınıfı zafiyetleri baştan engeller.
- Opt-in kapılar (--db-enable/--fs-enable, VARSAYILAN KAPALI) ADR-0012 disiplinini korur; broker audit JSONL tüm db/fs isteklerini kaydeder.

## Faz kabul kararı: **ACCEPTED**

## Sonraki faza geçiş: **READY**

- 5/5 zorunlu kriter PASS; kritik bulgu yok. IT-01/06/07/08 düzeltmeleri sonrası ikinci CI koşusunun yeşil dönüşü sahibin Faz 11 onayı için yeterlidir (`agent-dbfs` job'ı gerçek kanıtı üretir; gate'ler sessiz atlamayı yasaklar).
