# Threat Model (Initial)

## Assets

- Node control authority, host root-equivalent operations, Docker daemon and hypervisor access.
- Database records, user sessions, Agent certificates, API tokens, provider credentials, private keys and backups.
- Website, database and mail customer data managed by the operator.
- Integrity of node inventory, capability reports, task results and audit events.

## Adversaries

- Unauthenticated internet attacker.
- Compromised administrator/operator account.
- Compromised managed node or malicious local process.
- Malicious/misconfigured extension or dependency.
- Network attacker attempting MITM, replay or downgrade.
- Insider with excess privileges.

## High-risk attack paths

- Browser/API to arbitrary shell or Docker socket.
- Compromised Agent to other nodes or Control Plane.
- File manager path traversal or symlink escape.
- Malicious Docker image / Compose file mounting host paths or privileged mode.
- SSRF through provider/webhook/URL test functions.
- Stolen enrollment token or Agent private key.
- Backup restore to wrong target / data exfiltration.
- DNS/SSL misconfiguration causing service takeover or certificate abuse.

## Required mitigations

- RBAC, node scoping, MFA, secure cookies, CSRF defenses, rate limits and audit logs.
- mTLS, short-lived one-use enroll tokens, key rotation/revocation, protocol version pinning and replay protection.
- Typed operation allowlist, local privileged broker, strict schema validation and command-free interfaces.
- Docker-specific privilege warnings and policy checks for host mounts, privileged containers, host PID/network, devices and capabilities.
- Network egress policy and SSRF protections for integrations.
- Filesystem canonicalization, no-follow/openat-style safe access and isolated test cases.
- Secret envelope encryption and redaction; signed update manifests and binaries (Faz 15: SCP-SIG-V1 ed25519 — CP publish + node staging + health-check/rollback, all fail-closed; ADR-0020).
- Extension manifests are signed, start with NO permissions and require explicit per-permission admin grants; delegation is limited to registered typed operations (ADR-0019).
- Backups are verified and restore is rehearsed in isolated destination before production overwrite.

## Release blockers

Any unresolved Critical or High security finding in authentication, authorization, Agent identity, privilege boundary, Docker access, file access, secret management or update signature verification blocks production release.

## Release blocker status (Faz 15 closure)

- **Update signature verification:** CLOSED in v0.16.0-rc1 — three fail-closed layers proven by tests and CI (`TestRealUpdateLifecycle`, `TestStageRejectsUnsignedAndTampered`, `TestUpdateFamilyValidation`, `release-package` signature verification). Evidence: `docs/release/RELEASE-v0.16.0-rc1.md` §5-6.
- Remaining adversities (extension permission abuse, malicious node) are covered by the grant model, revocation paths and audit; no open Critical/High finding is carried.
