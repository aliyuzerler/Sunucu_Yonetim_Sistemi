# Project Status

- **Status:** Phase 02 executed — identity (argon2id + server-side sessions), one-time admin bootstrap, default-deny RBAC with node/group scopes, node inventory, PostgreSQL durable task/outbox engine with leasing worker, append-only audit with redaction, and a Playwright E2E layer with committed screenshots; awaiting CI run, independent test-prompt run and owner approval.
- **Current phase:** 02 — Identity, RBAC, node inventory, task/outbox (COMPLETE pending CI + owner approval) → next: 03 — Agent protocol, enrollment, mTLS, capability reporting.
- **Phase 02 evidence:**
  - Migrations `0002_identity_rbac`, `0003_inventory`, `0004_tasks_outbox`, `0005_audit` (embedded, transactional, reversible; audit is append-only via trigger).
  - Backend packages: `internal/identity` (argon2id, hashed server-side sessions, derived CSRF token, one-time bootstrap, in-memory login throttling), `internal/rbac` (pure default-deny policy + SQL store), `internal/inventory` (nodes/groups/tags/facts/capabilities), `internal/tasks` (typed operation registry, single-transaction task+outbox creation, `FOR UPDATE SKIP LOCKED` leasing worker, lease recovery, expiry sweep, outbox publisher), `internal/audit` (redaction + keyset queries), `internal/testdb` (shared PG test harness).
  - REST surface (`/api/v1`): `POST /bootstrap/admin`, `POST /auth/login`, `GET /auth/session`, `GET /auth/csrf`, `POST /auth/logout`, nodes CRUD + tags/facts/capabilities, node-groups, tasks create/list/get/cancel, `GET /task-operations`, `GET /audit`, `GET /users` (read-only), `GET /overview`. Session cookie `HttpOnly SameSite=Strict` (+ `Secure` via `CP_COOKIE_SECURE`); CSRF header required on mutations.
  - Bootstrap: `control-plane bootstrap` CLI issues a single-use, ≤1h token (hashed at rest); no HTTP token issuance; first admin + global admin binding in one transaction (race-tested).
  - Frontend: login page, auth gate + provider, Nodes (create + groups + tags), Tasks (typed registry, create/cancel/refresh, status badges), Audit log (filters + keyset "load more"), Users (read-only), honest Overview counts; TR/EN dictionaries extended; vitest coverage with route-based API mocks.
  - E2E (Playwright 1.57.0): 5 smoke specs (login, node create, task lifecycle, audit, EN locale) against mock API locally and the REAL control plane in CI (`E2E_REAL=1`); screenshots committed under `docs/reviews/screenshots/phase-02/`.
  - CI: new `e2e` job — PostgreSQL 18 service + Go build + `migrate up` + real bootstrap + Playwright (chromium) with artifact upload; `actions/upload-artifact` pinned by SHA (verified v4).
  - Dependencies added: `golang.org/x/crypto v0.58.0` (BSD-3-Clause, argon2id), `@playwright/test 1.57.0` (Apache-2.0, dev-only) — recorded in `docs/technology/dependency-registry.md`.
  - ADR-0007 (identity/sessions/bootstrap/CSRF) accepted; ADR index updated.
- **Phase 01 evidence:** monorepo skeleton, health endpoints, migration runner, React shell, compose, CI (see `docs/reviews/phase-01-test-report.md`).
- **Completed (Phase 00):** documentation audit, contradiction fixes, ADR-0006, ADR index, phase dependency map.
- **Not completed:** Agent protocol/enrollment (Phase 03), OpenAPI artifact + breaking-change CI (open item from Phase 02, start of Phase 03), user/role management UI (read-only view shipped; lifecycle later), proto generation, automated SBOM (Phase 15).
- **Known findings (Phase 02):** N-1 PG integration tests run in CI only (sandbox has no Docker/PostgreSQL); N-2 OpenAPI artifact pending; N-3 in-memory login rate limit (single-instance honest, revisit with HA); N-4 task execution is control-plane-internal until agents arrive. `npm audit` re-checked 2026-11-01: unchanged (12 moderate, eslint dev-only).
- **Environment constraints (sandbox):** Docker/PostgreSQL unavailable → compose and PG tests validated via CI service container; owner should run `make compose-up`, `make migrate-up`, bootstrap and `make web-e2e-real` locally for live verification. Go 1.27.3 and Node toolchain installed with official checksum verification.
- **Open decisions carried forward:** product license (before distribution, ≤ Phase 15), ACME implementation choice (Phase 09).
- **Primary initial targets:** Ubuntu Server 24.04 LTS and Debian 13.
- **Control Plane:** Linux-first, self-hosted (ADR-0005, ADR-0006).
- **Next action:** Owner reviews the Phase 02 report and `docs/reviews/phase-02-test-report.md`, watches the CI run (backend + frontend + e2e + secret-scan), optionally repeats the Playwright run locally, then approves Phase 03 (`prompts/phase-start/phase-03.md`).

ZCode must update this file at the end of every phase with evidence, commit/hash if available, tests passed/failed, unresolved issues and next phase readiness.
