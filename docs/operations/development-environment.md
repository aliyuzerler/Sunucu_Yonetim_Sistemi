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
- Integration tests: `TEST_DATABASE_URL="postgres://...?sslmode=disable" go test -race ./internal/db/migrate/` — skips with an explicit message when unset; runs against PostgreSQL 18 in CI.
- API: OpenAPI lint/compatibility validation — **planned with the first versioned API surface (Phase 02)**; CI has no OpenAPI artifact to validate yet.
- Proto: generation and breaking-change check — **Phase 03** (proto/ is reserved, empty by design).
- Security: secret scan (`scripts/secret-scan.sh`, CI job), `npm audit` (run at Phase 01: 12 moderate findings, dev-toolchain-only), Go vulnerability scanning and container image scan planned as dependencies arrive.

## Environment safety

Use mock adapters by default. Any real Agent installation or server change requires an explicit opt-in profile and a dedicated disposable test host. Do not expose the development UI to the public internet. The dev compose binds PostgreSQL to `127.0.0.1` only.
