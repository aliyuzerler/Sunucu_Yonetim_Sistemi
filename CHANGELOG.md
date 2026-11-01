# Changelog

## Unreleased

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
