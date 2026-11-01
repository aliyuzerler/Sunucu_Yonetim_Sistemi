# Faz 08 Bağımsız Test Raporu — Web hosting ve uygulama dağıtımı

**Test eden:** Bağımsız test ajanı (uygulama ajanı varsayımları olmadan, kanıt temelli)
**Tarih:** 2026-11-02 · **İncelenen commit:** c5b9323 (+ IT-01 düzeltmesi) · **Tag:** v0.9.0
**Yöntem:** `prompts/phase-test/phase-08-test.md` — kod incelemesi (file:line kanıtı), otomatik test taze koşusu, pozitif/negatif senaryo denetimi, önceki faz regresyonu, CI gerçek-ortam kanıtı.

## Zorunlu kabul kontrolleri

### 1. Site create/update/disable/delete typed operation olarak uygulanmalı — **PASS**

- 7 typed op backend registry'de: `web.site.list/status` (ops_faz08.go:254,261), `web.site.create/update/disable/delete` (ops_faz08.go:276,286,293,302), `web.php.list` — registry 33→**40 op** (service_test.go:216 `len(reg.Names()) != 40`).
- Agent tarafı relay-only executor: 7× `NewWebExecutor` (registry_unix.go:23-30); broker allowlist + strict şema (protocol.go:297-342); dispatch (exec_unix.go:178-205); aile `--web-enable` olmadan tipli `unsupported_operation` reddi (exec_unix.go:81-86, web_test.go:TestWebFamilyDisabledByDefault).
- Sınıf disiplini: create/update/disable `idempotent_desired_state` + retry-safe; delete `non_idempotent` + **retry-unsafe** (ops_faz08_test.go:TestFaz08RegistrySpecs — perm/class/retrySafe/target/timeout beşlisi ayrı ayrı doğrulanır).
- **Gerçek kanıt:** CI `agent-web` job'ı gerçek nginx 1.24 + php8.3-fpm üzerinde tam lifecycle koştu: `--- PASS: TestRealWebHostLifecycle (31.84s)` (run 41346840245) — create→update→disable→delete + rollback dosyası + docroot korunması doğrulandı.

### 2. Her site ayrı sistem kullanıcısı/izin ve güvenli document root ile izole edilmeli — **PASS**

- Site başına kullanıcı: `useradd --system --user-group --home-dir <webRoot>/<site> --create-home --shell /usr/sbin/nologin sc-web-<site>` (runner_linux.go:437-439); ad `sc-web-<name>` ≤31 karakter (webops.go:64,97-99, webops_test.go:TestSiteUserFitsUseraddLimit).
- Docroot `/var/www/server-control/<site>/html` — **0750**, sahip site kullanıcısı, grup www-data (runner_linux.go:467-493); site kullanıcıları birbirinin dosyasını okuyamaz; docroot override YOK (ad-türetimli — traversal yapısal olarak imkânsız; webops.go:97-99 + Hostile-name testi: TestInputNeverReachesArgvUnvalidated).
- PHP pool süreçleri site kullanıcısı altında çalışır; socket ACL yalnız www-data (render.go:99-107: `listen.owner/group = www-data`, `listen.mode = 0660`); `disable_functions` havuzu arındırılır (render.go:126).
- **Bulgudan düzeltilen kusur (CI-19):** `ensureSiteUser` home dizinini `paths()` dönüş sırası yüzünden **confDir'den** türetiyordu — CI gerçek-runner kanıtı (`/etc/nginx/conf.d/srvctl-citest` dizini, `rm: Is a directory`) ile yakalandı; home artık `webRoot/<site>` ve **useradd argv'si kesin doğrulayan regresyon testi** eklendi (runner_linux_test.go:TestCreateStaticHappyPath — tam argv eşlemesi).

### 3. Nginx config atomik yazılmalı, syntax check geçmeden reload yapılmamalı; rollback kopyası bulunmalı — **PASS**

- Atomik yazım: temp-dosya + fsync + `os.Rename` (runner_linux.go:202-253, atomicWrite).
- Reload kapısı: `nginx -t` (syntax, :517) → `nginx -T` dump'ta site metadata'sı (include kanıtı, :522-529) → ancak sonra `systemctl reload-or-try-restart nginx` (:531). Kapı başarısızsa `ErrSyntaxCheck`/`ErrNotIncluded` + **rollback geri yüklenir, reload sinyallenmez** (:548 undo; test: TestCreateSyntaxFailRollsBack `reload YAPILMAMALI` doğrulaması).
- Rollback kopyası: her içerik değişiminde önceki içerik `<path>.rollback` (:216-219); update sonucunda `RollbackPath` raporlanır (TestUpdateWritesRollbackCopy — ESKİ içerik byte-düzeyinde doğrulanır); delete öncesi de kopya (:808-810); disable syntax hatasında rename geri alınır (TestDisableSyntaxFailReverts).
- **IT-01 (bağımsız test bulgusu, ORTA) — düzeltildi:** disabled site update akışında eski `.disabled` config **yedeksiz siliniyordu**. Düzeltme: silmeden önce `disabledPath + .rollback` kopyası yazılır ve sonuçta raporlanır (runner_linux.go:716-726); regresyon testi (TestUpdateDisabledSiteKeepsRollback — byte-düzeyi eşleme) 11/11 paketle yeşil.

### 4. PHP-FPM pool ve PHP sürüm matrisi açıkça doğrulanmalı — **PASS**

- Kapalı matris {8.2, 8.3, 8.4, 8.5} (webops.go:66); matris dışı → `ErrPHPUnsupported` (TestValidatePHPVersion; backend aynası: webValidPHPVersion + negatif set).
- Matriste ama kurulu değil → `ErrPHPNotInstalled` — pool yazımından ÖNCE, hiçbir dosya dokunulmadan (runner_linux.go:176-180, 586-589; test: TestCreatePHPNotInstalled).
- Pool şablonu deterministik + sınanabilir: `[srvctl-<site>]`, pm dynamic + türetilmiş start/spare sınırları (1 çocuk → 1/1/1, render_test), socket path, ACL, chdir (render.go:91-129).
- Reload `php<v>-fpm.service` birimine (render.go:153; test: TestCreatePHPHappyPath reload argv doğrulaması). `web.php.list` matris vs kurulu tespiti + pool envanteri döndürür (TestPHPVersionsMatrix, TestPHPVersionsScansPools; CI: gerçek `php-fpm8.3` kurulu runner'da lifecycle PASS).

### 5. Apache ve Nginx port çakışması/edge proxy seçimi yönetilmeli; mevcut config üzerine kör yazılmamalı — **PASS**

- Dinleyici sahiplik tespiti: `/proc/net/tcp{,6}` parse + socket-inode → `/proc/<pid>/comm` eşlemesi (runner_linux.go:280-365; proc fixture testi TestListenersProcFixture — nginx sahipliği kanıtlanır).
- Karar matrisi: serbest → OK; nginx sahipli → OK (isim-tabanlı vhost); **Apache sahipli → tipli `ErrPortConflict` + edge-proxy önerisi** (render.go:284-296, mesaj açıkça "nginx'i edge proxy olarak farklı porta alın…" diyor); sahipsiz → güvenli tarafta red (TestCreatePortConflictApache, TestCreateUnknownOwnerRefused, TestSiteConflictErrorMessage).
- **Kör yazım yasağı:** yönetilen ad-uzayındaki markersız dosya `ErrForeignConfig` ile reddedilir, dosya bayt-düzeyinde DEĞİŞMEZ, reload yapılmaz (runner_linux.go:211; TestCreateForeignConfigRefused üç doğrulama). `nginx -T` include kanıtı include'ı etkisiz node'larda tipli `ErrNotIncluded` verir (sessiz yanlış yapılandırma yok).
- Apache driver arayüzü tasarlandı (ADR-0012 + web-hosting.md); adapter dürüstçe "tasarlandı, uygulanmadı" olarak belgelendi — kapsam kayması yok.

## Pozitif / negatif senaryo kapsamı

- **Pozitif:** static/php/proxy create, update+rollback, disable→list disabled gösterimi, delete docroot-koruyucu, list/status/php.list, broker-happy path (web_test.go:TestWebFamilyExecuteHappy), gerçek-host lifecycle (CI).
- **Negatif (reddedilir, yürütülmez):** allowlist dışı site adı (`-bad`, `Blog`, `a b`, `../etc`), boş/tekrarlı/geçersiz server_names, backend enum dışı, php matris dışı/kurulu değil, max_children sınırsız, proxy dış hedef (`10.0.0.5`, `0.0.0.0`, `example.com`, `unix:/tmp`, `..`), port 0/70000, bilinmeyen JSON alanı (strict decode), 8 KiB üstü payload, limit sınırı, yabancı config, çakışan port (apache/bilinmeyen), syntax hatası (rollback + reload yok), olmayan site (status/update/delete → `ErrNotFound`), ikinci disable (dürüst no-op), ikinci delete (`ErrNotFound`).
- **Altyapı düzeyi senaryolar** (yetkisiz kullanıcı, yanlış node, tekrar/replay, timeout, offline node): önceki fazların motor düzeyi testleri + broker SO_PEERCRED/UID allowlist + agent idempotency cache (task-id, 30 dk) + task timeout/retry-sınıfı kurallarıyla karşılanır; Faz 08 için yeni saldırı yüzeyi yoktur (aynı mTLS stream, aynı task zarfı; protokol değişmedi — buf breaking gate yeşil).

## Yerel taze koşu tablosu (bağımsız koşu, bu makine)

| Süit | Sonuç |
|---|---|
| agent `go vet ./...` + `gofmt -l` | PASS (temiz) |
| agent `go test -race -count=1 ./...` | PASS — **11/11 paket** (webops dahil: 30+ test) |
| `GOOS=windows vet + build` | PASS |
| backend `go vet` + `go test -race -count=1 ./...` | PASS — **11/11 paket** |
| web `eslint` + `tsc -b` | PASS |
| web `vitest run` | PASS — 5 dosya / **22/22 test** |
| web `npm run build` | PASS (vite 8) |
| Playwright (mock backend, tam suit) | PASS — **20/20** (web-hosting 3 test dahil) × bu koşu |
| secret-scan (`scripts/secret-scan.sh`) | temiz |
| buf lint + breaking | **NOT RUN** (yerel buf yok) — CI `Protokol` job'ı PASS (protokol değişmedi) |
| Gerçek nginx/php-fpm yerel koşu | **BLOCKED** (root/nginx yok) — CI `agent-web` job'ı PASS ile kapsanır |
| Gerçek dağıtım koşuları (rocky/alma) | **NOT RUN** yerel — CI PASS (regresyon) |

## CI kanıtı (gerçek ortam)

- Run **41346840245** (commit c5b9323, tag v0.9.0): **12/12 job success** — backend, frontend, agent×2 distro, agent-rhel×2 (rocky 9 / alma 9), agent-windows, **agent-docker**, **agent-web (yeni)**, e2e, proto, secret-scan.
- agent-web kanıt satırları: `nginx version: nginx/1.24.0 (Ubuntu)`, `=== RUN TestRealWebHostLifecycle`, `nginx reload sinyali verildi: /etc/nginx/conf.d/srvctl-citest.conf` (tek önek — CI-19 düzeltmesi sonrası), `--- PASS: TestRealWebHostLifecycle (31.84s)`, cleanup'ta `Is a directory` hatası YOK (CI-19 sonrası temiz).
- İlk koşu (run 41346187917, 839e4f2): 12/12 success ama log **CI-19 kusurunu ortaya çıkardı** (`srvctl-srvctl-citest.conf` çift önek + `/etc/nginx/conf.d/srvctl-citest` dizini) — düzeltildi, tag yeni commit'e taşındı, regresyon testi eklendi.

## Bulgular

| ID | Önem | Açıklama | Durum |
|---|---|---|---|
| CI-19 | **Yüksek** (gerçek-host davranış kusuru) | `ensureSiteUser` home dizini `paths()` sırası yüzünden confDir'den türetiliyordu → `useradd --create-home` nginx config dizini altına site dizini açıyordu | **DÜZELTİLDİ** (c5b9323) — argv-düzeyi regresyon testi; CI kanıtı temiz |
| IT-01 | Orta | Disabled site update'inde eski `.disabled` config yedeksiz siliniyordu (kriter 3 rollback ruhu) | **DÜZELTİLDİ** — `disabledPath + .rollback` kopyası + byte-düzeyi regresyon testi |
| IT-02 | Düşük/Bilgi | Rocky/AlmaLinux'ta nginx grubu `nginx`'tir; varsayılan `www-data` Debian/Ubuntu'ya göredir — Runner.NginxGroup alanı yapılandırmaya açık, destek beyanı ubuntu/debian ile sınırlı (web-hosting.md §Dürüst sınırlar) | Belgelendi (açık) |
| IT-03 | Bilgi | `Create` mevcut etkin site + sahipliği belirsiz dinleyici kombinasyonunda temkinli red verir (operatör update kullanır); davranış güvenli taraftadır | Belgelendi (açık) |
| IT-04 | Bilgi | mock-api worker log etiketi `ock-tick]` (Faz 07'den kalma yazım hatası) — test altyapısı, üretim etkisi yok | Belgelendi (açık) |

## Regresyon sonucu

Faz 00–07 süitleri taze koşuda tamamen yeşil: agent 11/11 (-race; dockerx/composeops/fwops/pkgops/svcops/winops dahil), GOOS=windows vet+build, backend 11/11 (-race; 40-op registry), web vitest 22/22, Playwright 20/20 (smoke, enrollment, telemetry, windows, linux-distro, docker, web-hosting), secret-scan temiz; CI'da gerçek distro + docker + windows + buf + e2e job'ları PASS.

## Güvenlik değerlendirmesi

- **Yürütme yüzü:** web ailesi YALNIZ privileged broker'da; `--web-enable` ile opt-in (varsayılan KAPALI; kapalıyken tipli unsupported). Agent yalnız doğrulayıp relay eder.
- **Girdi yüzeyi:** site adı tek tohum (allowlist); kullanıcı/yol/docroot ad-türetimli; docroot override yok; proxy hedefi yalnız loopback/unix (SSRF guard); argv disiplini (useradd/userdel/nginx/systemctl sabit vektör, shell yok); broker strict decode + `..` reddi; payload ≤8 KiB.
- **Değişiklik disiplini:** marker kanıtı olmadan kör yazım yok; her değişiklikte `.rollback`; syntax + include kanıtı olmadan reload yok; delete dosya-koruyucu (docroot asla silinmez); her broker isteği (red dahil) JSONL audit.
- **RBAC:** `web:read`/`web:control`; operator salt-okuma; migration 0012 yalnız yeni izinleri seed eder. Secret redaksiyonu: üretilen config/pool içeriğinde secret yoktur (şablon sabitleri + doğrulanmış alanlar).

## Faz kabul kararı

**ACCEPTED** — 5/5 zorunlu kriter PASS (her biri file:line + test + CI kanıtlı); bulgulardan Yüksek (CI-19) ve Orta (IT-01) düzeltildi ve regresyon testleriyle + taze CI koşusuyla doğrulandı; kritik/açık yüksek bulgu kalmadı.

**Sonraki faza geçiş: READY** — koşullar: zorunlu kriterlerin tamamı PASS, kritik/yüksek açık bulgu yok. (Faz 09'a yalnız kullanıcı onayı sonrası geçilmelidir — iş akışı kuralı.)

*Bulguların düzeltmeleri bağımsız testin ürettiği commit'lerde yer alır: IT-01 düzeltmesi bu raporla aynı commit'te; CI-19 düzeltmesi c5b9323'tedir.*
