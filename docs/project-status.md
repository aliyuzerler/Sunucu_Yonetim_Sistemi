# Project Status

- **Status:** Phase 01 executed — monorepo skeleton, Go API + React shell, PostgreSQL 18 dev environment and CI gates in place; awaiting independent test-prompt run and owner approval.
- **Current phase:** 01 — Monorepo, dev environment, base UI/API (COMPLETE pending owner approval) → next: 02 — Identity, RBAC, node inventory, task/outbox.
- **Phase 01 evidence:**
  - Monorepo: `backend/control-plane/`, `agent/`, `proto/`, `web/`, `deploy/`, `docs/` (agent & proto reserved with READMEs, by design).
  - Backend: `GET /healthz`, `GET /readyz` (DB ping → ready/degraded), `GET /api/v1/version`; chi router, structured slog (query strings never logged), security headers, request-ID propagation, JSON error envelope, body limit, graceful shutdown. Unit tests + race clean. Live smoke test performed.
  - Migrations: embedded SQL runner (`cmd/migrate up|down|status`), advisory-locked, transactional, reversible; integration tests against PostgreSQL 18 in CI (skip explicitly without `TEST_DATABASE_URL`).
  - Frontend: React 19.3 + strict TS 5.9.3 + Tailwind 4 tokens; cPanel-inspired shell (sidebar per `navigation.md`, top bar, breadcrumbs), TR/EN locale keys, honest empty states (no fake metrics), accessible status badges; lint + typecheck + 4 component tests + build all green.
  - Deploy: `deploy/docker-compose.yml` PostgreSQL 18-alpine pinned by digest, port bound to 127.0.0.1, `.env.example` placeholders only.
  - CI: `.github/workflows/ci.yml` — backend (fmt/vet/build/test-race with PostgreSQL 18 service), frontend (npm ci/lint/typecheck/test/build), secret scan; actions pinned by SHA.
  - Dependency records: `docs/technology/dependency-registry.md` (exact versions + SPDX licenses + risk notes).
- **Completed (Phase 00):** Documentation audit, contradiction fixes, ADR-0006, ADR index, phase dependency map (see `docs/reviews/`).
- **Not completed:** Identity/RBAC/node inventory/task engine (Phase 02), Agent protocol (Phase 03), OpenAPI contract artifact (starts Phase 02), proto generation & breaking-change CI (Phase 03), automated SBOM (Phase 15 packaging).
- **Known findings (Phase 01):** `npm audit` 12 × moderate — eslint toolchain only (dev-only, not shipped); tracked in dependency registry, re-checked each phase. TypeScript pinned to 5.9.3 (typescript-eslint 8.72.1 does not yet support TS 7.x).
- **Environment constraints (sandbox):** Docker unavailable → compose-up and live DB checks executed as code review + CI service container instead; compose behavior must be verified by owner locally (`make compose-up`). Node 26.13.0 and Go 1.27.3 installed with official checksum verification.
- **Open decisions carried forward:** product license (before distribution, ≤ Phase 15), ACME implementation choice (Phase 09).
- **Primary initial targets:** Ubuntu Server 24.04 LTS and Debian 13 (verify exact point releases and packages before implementation).
- **Control Plane:** Linux-first, self-hosted (ADR-0005, ADR-0006).
- **Next action:** Owner reviews the Phase 01 report and runs `prompts/phase-test/phase-01-test.md` independently (or approves this self-audit in `docs/reviews/phase-01-test-report.md`). On approval, start `prompts/phase-start/phase-02.md`.

ZCode must update this file at the end of every phase with evidence, commit/hash if available, tests passed/failed, unresolved issues and next phase readiness.
