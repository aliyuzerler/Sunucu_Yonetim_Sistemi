# Agent Architecture

## Process layout

- `server-agent`: unprivileged network identity, heartbeat, inventory, telemetry, task validation, plugin/adapter dispatch.
- `server-privd` (Linux) / `ServerControlBroker` (Windows): highly constrained local privileged broker.
- Optional OS-specific sidecars only when justified by compatibility or security evidence.

## Agent lifecycle

1. Admin creates a short-lived, single-use enrollment token scoped to a node/group.
2. Operator installs Agent using signed package/repository instructions.
3. Agent generates a private key locally; private key never leaves the node.
4. Agent exchanges token for client certificate over TLS; token is consumed atomically.
5. Control Plane binds certificate identity to immutable node ID and supported adapter metadata.
6. Agent opens outbound mTLS gRPC stream and sends heartbeat/capabilities.
7. Certificate is rotated before expiry; lost/compromised node can be revoked immediately.

## Protocol requirements

- Protocol Buffers messages have explicit version fields and operation IDs.
- Every message includes `request_id`, `node_id`, `issued_at`, `expires_at`, `idempotency_key`, trace/correlation ID and schema version where relevant.
- Reject old, expired, wrong-node, unsupported-version and unknown operations.
- Bound message size, concurrency, execution time and log output.
- Stream reconnect uses exponential backoff with jitter; respect server retry-after.
- Agent should continue collecting minimal local status while disconnected but must not queue privileged commands indefinitely without expiry.

## Local broker rules

- Broker interface accepts typed operations only (e.g., `ServiceRestart(service_id)`), never shell text.
- Allowlist is compiled/configured from signed policy; default deny.
- Each action independently checks OS version, capability and caller identity.
- Broker audit records include operation, actor/request identity, target, arguments after secret redaction, exit/result, timestamp and correlation ID.
- Service files/configuration must be written atomically; validate before activation; keep rollback copy.

## Windows Agent

- Use Windows Service Control Manager and Event Log APIs where possible.
- Protect named pipe with explicit ACLs and verify client identity.
- Do not expose arbitrary PowerShell execution. If PowerShell is needed, use fixed signed scripts with structured arguments and strict validation.
- Package/sign binaries and update manifests; support rollback.

## Upgrade and compatibility

Agent and server have independent versions. Control Plane negotiates the highest mutually supported protocol version. A minimum supported Agent version is enforced. Breaking changes require a new protocol version and migration window.
