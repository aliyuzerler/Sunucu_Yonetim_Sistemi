# Changelog

## Unreleased

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
