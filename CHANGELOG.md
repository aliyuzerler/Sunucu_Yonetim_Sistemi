# Changelog

## Unreleased

## [0.5.0] — 2026-11-01 — Faz 04: Linux Agent ve temel OS yönetimi

### Added
- Ayrıcalık ayrımı: `server-agent` (yetkisiz) + `server-privd` (root broker) ikilileri; broker yalnızca Unix socket (`/run/server-control/privd.sock`, 0660, dizin 0750) dinler, TCP yok; SO_PEERCRED ile UID allowlist (default deny); tipli JSON istekler; her istek (reddedilenler dahil) JSONL audit'e yazılır (`deploy/agent/` systemd unitleri + sertleştirme).
- OS tespiti (`agent/internal/osdetect`): /etc/os-release (ID/VERSION_ID/PRETTY_NAME/HOME_URL), çekirdek sürümü (procfs), systemd varlık+sürüm (procfs işaretleri + `systemctl --version` argv), paket yöneticisi (apt/dpkg/dnf/yum/unknown); kaynak provenance'ı ile raporlanır.
- Metrikler (`agent/internal/metrics`): CPU (pencere bazlı %, çekirdek, load 1/5/15), bellek, disk (gerçek FS filtre + cihaz dedupe + statfs kullanım), ağ (lo hariç), uptime — procfs/statfs kaynağı ve collection timestamp'i ile; protokol v1'e geriye dönük `SystemFactsReport` + `MetricsReport` zarf yükleri (field 14/15) ve `HelloAck.metrics_interval_seconds` (sunucu önerisi 60s; agent 10s–10dk kıskacı) eklendi.
- Servis işlemleri (`agent/internal/svcops`): systemd list/status/start/stop/restart — birim adı allowlist'i `^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`, exec.Command argv (shell string'i YOK), işlem başına timeout, sınırlı çıktı; broker üzerinden `linux.systemd.service.*` operasyonları.
- Capability beyanı: broker erişilebilirliği rapor zamanında probe edilir; servis kabiliyetleri erişilemezse `reason_unavailable` ile bildirilir (runtime contract, capability-system.md).
- Control plane: migration `0008_node_telemetry` (node_system_facts upsert + node_metrics zaman serisi + indeksler + retention 7g), `internal/telemetry` servisi (doğrulama+sınır+prune), agentsrv `SystemFactsReport`/`MetricsReport` işleyicileri (node başına 10s hız sınırı; metrics veri-düzlemi olduğu için tek tek audit'lenmez), REST `/nodes/{id}/facts/system`, `/nodes/{id}/metrics/latest`, `/nodes/{id}/metrics` (nodes:read).
- Görev motoru: 5 yeni node-hedefli operasyon (`linux.systemd.service.list/status/start/stop/restart`) — payload validator'ları iki tarafta (sunucu + agent), `OperationSpec.RequiredPerm` ile RBAC katmanı: okuma `service:read`, start/stop/restart `service:control` (tasks:write üstüne); restart `non_idempotent` + retry-güvensiz.
- RBAC: `service:read` ve `service:control` izinleri (migration 0008 seed; admin=hepsi, operator=yalnız okuma — least privilege).
- UI: Node detay görünümü — sistem bilgileri kartı, metrik kartı (progressbar'lı disk kullanımı, kaynak+toplanma zamanı), kabiliyet rozetleri (nedeniyle), tipli servis eylem formları (istemci tarafı birim doğrulama; gerçek denetim sunucu+agent+broker'da); TR/EN sözlükler.
- CI: `agent` job iki distro matrisi (ubuntu-24.04 runner + debian:13 container) — Faz 04 "iki distro test matrisi" teslimatı.
- Testler: osdetect fixture matrisi (Ubuntu 24.04 + Debian 13), metrics parser testleri, svcops fake-systemctl argv/timeout/eksik-systemd testleri, broker socket entegrasyonu (UID reddi, allowlist, shell-meta birim reddi, audit zorunluluğu, hız sınırlı eşzamanlılık), agentsrv telemetry entegrasyon testi (facts upsert + metrics hız sınırı), tasks validator birim testleri, httpapi uç testleri, NodeDetail vitest (4 test), E2E telemetry spec (2 test) + 3 ekran görüntüsü.

### Changed
- `agent.DefaultRegistry(brokerSocket)` imzası; `Run(ctx, store, addr, executors, RunOptions{FactsRoot})`; `server-agent run --broker-socket` bayrağı (varsayılan `/run/server-control/privd.sock`).
- `docs/technology/dependency-registry.md`: `golang.org/x/sys` v0.48.0 indirect→direct (BSD-3-Clause); yeni HARİCİ bağımlılık eklenmedi.
- `agent/README.md` + `deploy/agent/README.md` kurulum ve güvenlik modeliyle güncellendi.

### Security
- İşlem girdileri asla shell string'ine birleştirilmez: broker tipli JSON, systemctl argv; birim adı regex allowlist'i iki uçta + broker'da üç kez doğrulanır (sunucu validator, agent executor, broker validateRequest).
- Broker sadece root tarafından yazılabilir socket üzerinden erişilir; SO_PEERCRED allowlist dışındaki her UID `peer_denied` ile reddedilir ve auditlenir; allowlist boşsa tamamen kapalı.
- Agent process'i systemd sertleştirmesiyle çalışır (NoNewPrivileges, ProtectSystem=strict, boş CapabilityBoundingSet); broker yalnız AF_UNIX'e kısıtlıdır.
- Metrik yutumu node başına 10s hız sınırıyla korunur; snapshot alan sınırları (≤64 disk/ağ, kaynak adı ≤128) sunucu tarafında zorlanır.
- `service:control` izni ayrı tutulur: görev oluşturma yetkisi olan operatör varsayılan olarak servis durduramaz/yeniden başlatamaz.

## [0.4.0] — 2026-11-01 — Faz 03: Agent protokolü, kayıt, mTLS ve capability

### Added
- Protokol sözleşmesi: `proto/servercontrol/agent/v1/agent.proto` — `EnrollService` + `AgentService.Connect` (bidi stream); zarflar request_id/node_id/correlation_id/issued_at/expires_at/idempotency_key taşır; `buf.yaml` lint/breaking; üretilen kod `gen/proto` (ayrı modül); `make proto-tools/proto/proto-check` (CI kırılma kapısı).
- Enrollment (ADR-0008): tek kullanımlık, ≤1h, node/group kapsamlı token'lar (yalnızca SHA-256 özeti saklanır); tüketim + node devri/oluşumu + sertifika kaydı TEK transaction'da (FOR UPDATE + replay koruması); plaintext token yalnızca üretim anında bir kez döner.
- mTLS kimlik: operatör CA'sı (`control-plane ca init`, anahtar 0600, DB'de asla değil); agent anahtarı LOKALDE üretilir, yalnızca CSR iletilir; leaf sertifikaya sunucu tarafında URI SAN node kimliği yazılır; gRPC sunucusu istemci sertifikası + DB revocation + node durumu denetimi yapar; her zarfta node_id ↔ sertifika kimliği eşleşmesi zorunlu.
- Stream: Hello→HelloAck (v1 anlaşması), heartbeat (last_seen/active), capability raporu → node_capabilities upsert; süresi geçmiş zarflar atılır.
- Görev dağıtımı: tasks tablosuna `execution_target` (control_plane|node); node-hedefli görevler canlı stream üzerinden SKIP LOCKED + 60s lease ile dağıtılır; `OperationResult` (SUCCEEDED/FAILED/REJECTED) yalnızca dispatched durumda ve doğru node'dan uygulanır.
- Agent ikilisi (`server-agent`, ayrı Go modülü): lokal ECDSA anahtar (0600) + CSR, CA pinlemeli enroll (MITM guard), outbound mTLS koşu döngüsü (backoff+jitter), allowlist executor'lar (`agent.echo`) ve sınırlı idempotency cache; bilinmeyen operasyon REJECTED.
- HTTP API + UI: `/api/v1/agent-enrollments` (list/issue/revoke) ve "Agent Kaydı" sayfası (tek gösterim paneli, durum listesi, iptal); `enrollment` menü öğesi Faz 03'ten aktife alındı.
- CI: `proto` job (buf lint + üretim + git diff kapısı) ve `agent` job (vet/build/test-race).
- Testler: certs birim testleri; agent birim testleri; agentsrv PostgreSQL entegrasyon testleri (enroll akışları, sertifikasız/yanlış-node bağlantı reddi, uçtan uca dispatch); E2E'ye Agent Kaydı senaryosu (6/6).

### Changed
- `docs/decisions/ADR-0008-agent-protocol-mtls.md` ve ADR dizini; `docs/technology/dependency-registry.md` (grpc v1.85.0, protobuf v1.36.12, pinli buf/plugin araç zinciri); `docs/operations/development-environment.md` (CA/enroll/agent akışları); proto/ ve agent/ README'leri gerçek içerikle dolduruldu; README bileşen tablosu.

### Security
- Özel anahtar node'dan asla çıkmaz; CSR dışındaki malzeme reddedilir; sertifikada kimlik sunucu tarafından atanır (agent kimliğini seçemez); token/sertifika hash'leri DB'de; revoke edilmiş sertifika bağlantı kuruluşunda reddedilir; zarf expiry her iki yönde uygulanır; payload üst sınırları (64KiB) ve allowlist doğrulamaları her iki uçta.

## [0.3.0] — 2026-11-01 — Faz 02: Kimlik, RBAC, node envanteri, görev altyapısı ve E2E

### Added
- Kimlik ve oturum (ADR-0007): argon2id parola özeti; sunucu taraflı oturumlar (token yalnızca SHA-256 özeti saklanır); `HttpOnly SameSite=Strict` cookie; oturum token'ından türetilen CSRF token'ı (mutating isteklerde `X-CSRF-Token` zorunlu); IP/kullanıcı bazlı giriş hız sınırlama; uniform hata ile kullanıcı adı sızıntısının önlenmesi.
- İlk yönetici bootstrap: `control-plane bootstrap` CLI ile tek kullanımlık, kısa ömürlü token (≤1h, yalnızca özeti saklanır); `POST /api/v1/bootstrap/admin` tek transaction'da ilk yönetici + global admin rolü oluşturur; yarış testiyle tek admin kanıtı; varsayılan parola yok.
- RBAC: sabit izin kataloğu (users/nodes/tasks/audit × read/write), `all`/`group`/`node` kapsamlı rol bağlamaları, default-deny policy (pure fonksiyon + SQL store), her uçta `requirePermission`, reddedilen isteklerin audit'i.
- Node envanteri: nodes/node_groups/node_tags/node_facts (sürümlenmiş JSONB)/node_capabilities tabloları; oluşturma, güncelleme, etiket, grup ataması, facts upsert, capability upsert; keyset sayfalama ve izin filtreleri.
- Görev altyapısı: tipli operasyon kaydı (`noop.echo`, `inventory.node.tag.set` — allowlist, payload şema doğrulaması); task + outbox TEK transaction'da; worker `FOR UPDATE SKIP LOCKED` kiralaması, lease kurtarma, süre aşımı taraması, outbox teslimi (exponential backoff); attempt kayıtları; idempotency key (kısmi benzersiz indeks + replay/çatışma semantiği).
- Audit: append-only `audit_events` (trigger ile UPDATE/DELETE engeli), actor/target/action/result/correlation ID, secret redaction (recall-first, test edildi), keyset sayfalama.
- REST `/api/v1`: bootstrap, auth (login/session/csrf/logout), nodes (+tags/facts/capabilities), node-groups, tasks (+cancel, task-operations), audit, users (salt-okunur), overview özeti.
- Frontend: giriş sayfası + auth gate/provider; Sunucular (oluşturma + grup + etiket), Görevler (tipli kayıt, oluştur/iptal/yenile), Denetim Kaydı (filtre + "daha fazla"), Kullanıcılar (salt-okunur), gerçek sayılarla dürüst Genel Bakış; TR/EN sözlük genişletmesi.
- Playwright E2E katmanı: 5 smoke senaryosu (giriş, node oluşturma, görev yaşam döngüsü, denetim kaydı, EN yerel ayar); lokalde mock API (`web/e2e/mock-api.mjs`), CI'da gerçek Control Plane (`E2E_REAL=1`); ekran görüntüleri `docs/reviews/screenshots/phase-02/` altında repoya commit edildi.
- CI `e2e` job'ı: PostgreSQL 18 servis + Go build + `migrate up` + gerçek bootstrap + Playwright (chromium) + artifact yükleme (`actions/upload-artifact` SHA-pinli, doğrulanmış).
- `docs/decisions/ADR-0007-identity-sessions-bootstrap.md`; ADR dizin güncellemesi; `internal/testdb` ortak PostgreSQL test altyapısı; Makefile `web-e2e`/`web-e2e-install`/`web-e2e-real` hedefleri.

### Changed
- Migration seti 0002–0005 ile genişledi (kimlik, envanter, görev, denetim).
- `docs/ui-ux/navigation.md` — Operasyonlar grubuna "Audit Log" eklendi (Faz 02 denetim görünürlüğü).
- `docs/technology/dependency-registry.md` — `golang.org/x/crypto v0.58.0` (BSD-3-Clause), `@playwright/test 1.57.0` (Apache-2.0, dev-only) kayıtları.
- `docs/operations/development-environment.md` — bootstrap akışı, E2E komutları ve yeni ortam değişkenleri.
- `docs/project-status.md` — Faz 02 kanıtları ve açık işler.

### Security
- Oturum/CSRF token'larının veritabanında düz metin bulunmaması; audit ve loglarda secret redaction; audit tablosunun append-only olması; bootstrap token üretiminin yalnızca host CLI'sında olması; default-deny RBAC ve katalog dışı izin reddi; payload `DisallowUnknownFields` + boyut limitleri; login rate limit + uniform hata.

### Fixed (CI doğrulama turunda)
- CI-1: Paralel test paketlerinin migration advisory lock yarışı — `migrate` artık sınırlı süre bekler (30s), sonra yüksek sesle hata verir; lock testi iptal yolunu da kapsar.
- CI-2: `PGRecorder.Record` merkezî secret redaction uygular (defense in depth).
- CI-3: CI e2e job'ı `playwright install --with-deps` takılması — runner imajındaki system Chrome kullanılır (`E2E_BROWSER_CHANNEL=chrome`); job'lara `timeout-minutes` eklendi.
- CI-4: Her test paketi `internal/testutil.UniqueDB` ile kendi izole disposable veritabanını kullanır (per-test DB'ler test sonunda DROP WITH FORCE ile kaldırılır).

## [0.2.0] — 2026-11-01 — Faz 01: Monorepo, geliştirme ortamı, temel UI/API

### Added
- Monorepo yerleşimi: `backend/control-plane/`, `agent/` (Faz 03 rezerve), `proto/` (Faz 03 rezerve), `web/`, `deploy/`.
- Go Control Plane iskeleti: `GET /healthz`, `GET /readyz` (DB ping, degraded durumu), `GET /api/v1/version`; chi router; güvenlik başlıkları; istek-ID yayılımı; JSON hata zarfı; gövde limiti; graceful shutdown; maskelemeli yapılandırma (`config.RedactDatabaseURL`).
- Explicit migration altyapısı: gömülü SQL dosyaları, advisory-lock, transactional up/down, `migrate status`; `0001_baseline` migration'ı; PostgreSQL 18 entegrasyon testleri (CI servis konteyneri; `TEST_DATABASE_URL` yoksa açık SKIP).
- React 19.3 + TypeScript 5.9 (strict) + Tailwind 4 token kabuğu: `navigation.md` uyumlu sol menü, üst bar, TR/EN yerel anahtarları (derleme zamanı anahtar eşleşmesi), dürüst boş durumlar, erişilebilir durum rozetleri.
- `deploy/docker-compose.yml`: PostgreSQL 18-alpine sha256 digest ile pinli, port `127.0.0.1`'e bağlı; `.env.example` yalnızca placeholder.
- CI (`.github/workflows/ci.yml`): backend fmt/vet/build/test-race (+ PostgreSQL 18 servis), frontend npm ci/lint/typecheck/test/build, secret-scan; actionlar SHA ile pinli.
- Kök `Makefile` (backend/web/compose/migrate hedefleri) ve `scripts/secret-scan.sh`.
- `docs/technology/dependency-registry.md` — pinli sürümler + SPDX lisansları + risk notları.
- `docs/reviews/phase-01-test-report.md` — Faz 01 öz-denetim raporu.

### Changed
- `docs/operations/development-environment.md` — Faz 01 kararları (npm, Go 1.27.3, Node 26.13.0, nihai komut adları).
- `docs/project-status.md`, `CHANGELOG.md`, kök `README.md` — Faz 01 durumu ve hızlı başlangıç.

### Security
- `.gitignore` secret desenleri; CI secret taraması; `CP_DATABASE_URL` loglarda maskelenir; query string'ler asla loglanmaz; API güvenlik başlıkları (CSP, nosniff, DENY, no-store).

## [0.1.0] — 2026-11-01 — Faz 00: Mimari ve proje sözleşmesi

### Added
- `docs/reviews/phase-00-architecture-review.md` — tam dokümantasyon denetimi, bulgular F-01…F-06 ve çözümleri.
- `docs/reviews/phase-00-test-report.md` — bağımsız test promptu kriterlerine göre öz-denetim raporu.
- `docs/decisions/ADR-0006-multi-node-strategy.md` — çoklu-node stratejisi: ilk dağıtım tek Control Plane, çoklu-node hazır sözleşmeler, ölçüme bağlı ölçekleme yolu.
- `docs/decisions/README.md` — ADR süreci, şablonu ve ADR-0001…0006 dizini.
- `docs/phases/README.md` içine 16 fazın bağımlılık sırası tablosu (teknik bağımlılık + giriş/çıkış kriteri tanımları).
- `.gitignore` — gizli dosya ve derleme çıktılarının repoya girmemesi için temel kural seti.

### Changed
- `docs/architecture/system-architecture.md` — Failure model görev durum listesine `accepted` eklendi; task-lifecycle dokümanıyla hizalandı (F-01).
- `docs/vision/scope.md` — MVP ↔ faz eşleme notu eklendi (MVP = Faz 00–04; Docker sınırlı yaşam döngüsü Faz 07) (F-02).
- `docs/ui-ux/design-system.md` — Operations menü grubuna "Tasks & Activity" eklendi; navigation.md ile hizalandı (F-03).
- `docs/project-status.md` — Faz 00 durumu, kanıtlar, açık kararlar ve ortam riskleriyle güncellendi.
- `README.md` — proje deposu olarak yeniden düzenlendi; faz işleyişi ve bağımsız test promptu gereksinimi korundu.

### Notes
- Faz 00'da kod, bağımlılık veya migration yoktur; kit taban dokümantasyonu (`PACKAGE-MANIFEST.json`, `SHA256SUMS`) değişmeden korunmuştur.
