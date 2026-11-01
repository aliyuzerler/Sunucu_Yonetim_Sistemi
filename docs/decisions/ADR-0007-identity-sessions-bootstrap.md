# ADR-0007: Identity, Sessions, First-Admin Bootstrap and CSRF Model

- Status: Accepted (Phase 02)
- Context: Phase 02 requires a secure, one-time first-admin bootstrap with no default password, server-enforced RBAC (default deny, node/group scopes) and browser session security (secure cookies, CSRF protection, rate limiting). The control plane is initially a single instance (ADR-0006) and the product is self-hosted on the owner's infrastructure.

## Decision

1. **Password hashing is argon2id** (RFC 9106 profile m=19456 KiB, t=2, p=1 via `golang.org/x/crypto/argon2`), stored as a standard PHC string. Verification recomputes under stored parameters and compares in constant time. Minimum password length is 12; there is no default or seeded password anywhere.
2. **Server-side sessions, opaque tokens.** The browser receives a 256-bit URL-safe session token in an `HttpOnly; SameSite=Strict` cookie (`srvctl_session`; `Secure` controlled by `CP_COOKIE_SECURE`). Only the SHA-256 digest of the token is stored (`sessions.token_hash`); a stolen database backup cannot be replayed as a session. Session TTL is configurable (`CP_SESSION_TTL`, default 12h, max 30d).
3. **Derived CSRF token (no storage).** The CSRF token is `sha256("srvctl-csrf:v1:" || session_token)`. It is delivered once in the login response and re-derivable via `GET /api/v1/auth/csrf` for the holder of the cookie. Every mutating request must send `X-CSRF-Token`; the server compares in constant time. Rationale: cross-site attackers cannot read cross-origin responses, so they cannot obtain the derived value; SameSite=Strict blocks cookie attachment on cross-site requests as a second layer. Storing nothing avoids CSRF-token lifecycle tables and survives restarts.
4. **One-time bootstrap via CLI only.** `control-plane bootstrap` (run on the control plane host by an operator) issues a 256-bit single-use token; only its SHA-256 digest is stored with a short TTL (`CP_BOOTSTRAP_TTL`, default 15m, max 1h). `POST /api/v1/bootstrap/admin` consumes the token and creates the first admin user plus the global `admin` role binding in ONE transaction; the "no users exist" gate is re-checked inside that transaction, so a concurrent race cannot produce two admins (proven by a parallel-racer integration test). No HTTP endpoint ever ISSUES bootstrap tokens — first-run privilege escalation requires host access.
5. **RBAC is default-deny with scoped bindings** (`internal/rbac`): permissions are a fixed catalog (`users:read`, `users:write`, `nodes:read`, `nodes:write`, `tasks:read`, `tasks:write`, `audit:read`); bindings carry scope `all`, `group` (node is member of group) or `node`. Global resources (users, audit) require an `all`-scoped binding. Unknown permission names are rejected (typo/injection guard). Denied calls are audited with the policy reason.
6. **Login throttling is in-memory** fixed-window per IP and per username (20/min and 8/min). This is a brute-force brake, not a distributed quota; it is honest for the single-instance deployment of ADR-0006 and must move to a shared store together with any HA work. Failures are uniform (`invalid_credentials`) so username enumeration is not possible; successes reset the counter.
7. **Audit is append-only at the database.** `audit_events` blocks UPDATE/DELETE via trigger; the application layer redacts sensitive metadata keys (password/secret/token/credential/key/auth/cookie/session components, case-insensitive) before insert — recall-first redaction, tested.
8. **Typed operations over the task engine.** The task registry is an allowlist (`noop.echo`, `inventory.node.tag.set` in Phase 02); every create validates operation, version and payload schema. Agent-dispatched operations (Phase 03+) reuse the same registry contract; arbitrary shell execution remains impossible by construction.

## Consequences

- Session revocation is a database flag; logout and admin revocation are immediate on the next request.
- Restarting the control plane does not invalidate sessions (digests are in PostgreSQL) and does not require CSRF state.
- The in-memory rate limiter resets on process restart; the documented threat (credential brute force) is still bounded by uniform errors and per-window limits.
- Password parameter changes require a migration path (`password_algo` column exists for future rehash-on-login).
- The bootstrap CLI prints the token once to stdout; operators must treat it as a secret. Re-issue after TTL expiry is allowed only while no user exists.
