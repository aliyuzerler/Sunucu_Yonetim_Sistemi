# Development Environment

## Recommended local setup

- Git repository opened at the root folder in ZCode.
- Go 1.27.x, Node.js 26 LTS, package manager selected and pinned (npm or pnpm; choose one in Phase 01).
- Docker Engine + Docker Compose for local PostgreSQL 18 and optional test dependencies.
- PostgreSQL data stored in a named development volume; never use production DB.
- `.env.example` contains placeholders only. Actual `.env` is gitignored.

## Phase 01 decisions (2026-11-01)

- **Package manager: npm** (lockfile `web/package-lock.json`, lockfile v3). pnpm was the alternative; npm chosen for zero extra tooling and CI parity. Record in `docs/technology/dependency-registry.md`.
- **Toolchain pins:** Go **1.27.3** (`go.mod` `go 1.27.3`, CI `go-version-file`), Node **26.13.0** (LTS, `web/package.json` engines + CI), npm 11.x bundled with Node 26.
- **PostgreSQL 18 dev image:** `postgres:18-alpine` pinned by sha256 digest in `deploy/docker-compose.yml` (2026-11-01) and the CI service container.
- **Frontend strictness:** TypeScript strict + `noUnusedLocals/Parameters`, `exactOptionalPropertyTypes`, `verbatimModuleSyntax`; ESLint flat config with typescript-eslint recommended.

## Required commands (final names fixed in Phase 01)

Root `Makefile` targets (see also `deploy/README.md` quickstart):

- Backend (`backend/control-plane`): `make backend-build`, `make backend-test` (`go test ./...`), `make backend-race` (`go test -race ./...`), `make backend-vet` (`go vet ./...`), format gate `gofmt -l .` (must be empty; enforced in CI).
- Frontend (`web/`): `make web-install` (`npm ci` from lockfile), `make web-lint`, `make web-typecheck`, `make web-test`, `make web-build`.
- Database: `make compose-up` / `compose-ps` / `compose-down`; migrations: `make migrate-up`, `make migrate-status`, `make migrate-down` (uses `CP_DATABASE_URL`).
- Integration tests: `TEST_DATABASE_URL="postgres://...?sslmode=disable" go test -race ./...` — skip with an explicit message when unset; runs against PostgreSQL 18 in CI.

## Phase 02 additions (2026-11-01)

- **First-admin bootstrap:** migrate sonrası `go run ./cmd/control-plane bootstrap` (veya derlenmiş binary ile `control-plane bootstrap`) tek kullanımlık token üretir; `POST /api/v1/bootstrap/admin` ile ilk yönetici oluşturulur. Varsayılan parola yoktur (ADR-0007).
- **E2E (Playwright):**
  - Kurulum: `make web-e2e-install` (chromium indirir).
  - Mock API ile lokal: `make web-e2e` (önce build; `web/e2e/mock-api.mjs` + `vite preview`).
  - Gerçek backend: Control Plane'i `CP_DATABASE_URL` ile çalıştırıp bootstrap ile admin oluşturun, ardından `make web-e2e-real` (`API_PROXY_TARGET` default `http://127.0.0.1:8080`).
  - CI, e2e job'ında gerçek backend + PostgreSQL 18 ile çalıştırır; ekran görüntüleri artifact olarak yüklenir ve `docs/reviews/screenshots/phase-02/` repoda tutulur.
- **Environment variables (yeni):** `CP_COOKIE_SECURE` (prod TLS arkasında `true`), `CP_SESSION_TTL` (5m..720h), `CP_BOOTSTRAP_TTL` (1m..1h).
- API: OpenAPI lint/compatibility validation — **Phase 02 REST yüzeyi tanımlı; OpenAPI artefaktı ve CI doğrulaması bir sonraki faz başlangıcında eklenir (açık iş)**.
- Proto: generation and breaking-change check — **Phase 03** (proto/ is reserved, empty by design).
- Security: secret scan (`scripts/secret-scan.sh`, CI job), `npm audit` (run at Phase 01: 12 moderate findings, dev-toolchain-only), Go vulnerability scanning and container image scan planned as dependencies arrive.

## Environment safety

Use mock adapters by default. Any real Agent installation or server change requires an explicit opt-in profile and a dedicated disposable test host. Do not expose the development UI to the public internet. The dev compose binds PostgreSQL to `127.0.0.1` only.
