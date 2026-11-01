# Security Boundaries and Privilege Model

## Trust zones

1. Browser: untrusted input; no direct access to Agent, Docker socket, SSH or host privileged APIs.
2. Control Plane: authenticated application boundary; may enqueue typed actions but must not run node commands directly.
3. Agent network process: authenticated remote endpoint; still treated as potentially compromised.
4. Local privileged broker: narrow, validated privilege boundary on node.
5. Host services: Docker daemon, hypervisor, DB, web server, firewall and OS.

## Core rules

- No root credentials in frontend, API responses, logs or job payloads.
- No web endpoint that runs caller-provided shell/PowerShell commands.
- No public Docker socket or unauthenticated Docker TCP endpoint.
- mTLS for Agent identity; certificate revocation and rotation.
- CSRF protection for cookie-authenticated browser API; secure, HttpOnly, SameSite cookies.
- MFA for privileged accounts; reauthentication for sensitive operations.
- RBAC plus node/group resource scope; default deny.
- Rate limits and lockout/backoff for authentication/enrollment.
- Strict content security policy, security headers and dependency scanning.
- File manager resolves canonical paths and rejects traversal/symlink escapes.
- Uploads: size/type limits, temp file, malware scan hook, atomic move and permission policy.
- Logs redact credentials, tokens, private keys, database passwords and Authorization headers.

## Destructive actions

Examples: deleting VM/volume, wiping storage, removing database, changing firewall rules, resetting SSH, deleting DNS zone, restoring backup over production. Require: permission, target summary, impact preview, typed confirmation or reauthentication as appropriate, idempotency, audit, and verified rollback/backup path where feasible.

## Threat scenarios to test

- Stolen enrollment token; replayed token; cloned Agent identity.
- Agent certificate compromised or revoked node reconnects.
- Cross-node task substitution.
- Malicious user submits shell metacharacters in service/site/path/DB identifiers.
- Docker API access escalates to host root.
- Path traversal, symlink race and arbitrary file read/write.
- Job replay, task duplication, stale command execution and timeout confusion.
- Malicious plugin with excessive permissions.
- SSRF through URL-based health checks or webhook integrations.
- Database backup contains secrets; restore leaks or overwrites unrelated data.
