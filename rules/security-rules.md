# Security Rules

- Deny by default; least privilege; MFA for privileged actions.
- Never accept arbitrary shell, SQL, PowerShell or filesystem path as privileged instruction.
- Never expose Docker socket or libvirt privileged socket to the browser/public network.
- Protect every Agent connection with mTLS and certificate revocation.
- Apply validation at Control Plane, Agent and privileged broker boundaries.
- Keep secrets out of logs, events, frontend state persistence and repository.
- Every sensitive mutation produces an audit event.
- Destructive actions require preview and confirmation.
- Security-critical tests are release blockers.
