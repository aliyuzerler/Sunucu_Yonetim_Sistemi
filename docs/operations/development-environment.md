# Development Environment

## Recommended local setup

- Git repository opened at the root folder in ZCode.
- Go 1.27.x, Node.js 26 LTS, package manager selected and pinned (npm or pnpm; choose one in Phase 01).
- Docker Engine + Docker Compose for local PostgreSQL 18 and optional test dependencies.
- PostgreSQL data stored in a named development volume; never use production DB.
- `.env.example` contains placeholders only. Actual `.env` is gitignored.

## Required commands (final names may be fixed in Phase 01)

- Backend: `go test ./...`, `go vet ./...`, `go test -race ./...`
- Frontend: install from lockfile, `typecheck`, `lint`, `test`, `build`
- API: OpenAPI lint/compatibility validation
- Proto: generation and breaking-change check
- Integration: ephemeral test database, migrations up/down where supported, contract tests
- Security: dependency audit, secret scan, static analysis and container image scan

## Environment safety

Use mock adapters by default. Any real Agent installation or server change requires an explicit opt-in profile and a dedicated disposable test host. Do not expose the development UI to the public internet.
