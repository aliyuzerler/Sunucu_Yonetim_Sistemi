# Agent Rules

- Agent is a separate binary/service from Control Plane.
- Agent initiates outbound TLS/mTLS connections only.
- Reject untrusted node IDs, expired tasks, schema mismatch, unsupported operation and replay.
- Privileged broker uses a narrow local IPC surface and allowlisted typed operations.
- Never implement `Exec(command string)` or `RunShell(script string)` as a remote operation.
- Limit CPU, memory, log output, file size, worker concurrency and task duration.
- Upgrade artifacts must be signed and version/policy checked; rollback is supported.
