# Data Model Baseline

Core entities (names are conceptual; Phase 01/02 may refine exact schema):

- `users`: identity, status, MFA metadata, created/updated timestamps.
- `roles`, `permissions`, `role_permissions`, `user_role_bindings`: authorization model.
- `groups`: node grouping and policy scopes.
- `nodes`: immutable ID, display name, OS family/version, architecture, agent version, status, last seen, enrollment status, tags.
- `node_certificates`: fingerprint, serial, not-before/not-after, status, revoked-at; never store private keys.
- `node_capabilities`: node ID, capability ID/version, adapter version, status, observed-at, metadata JSON with strict schema.
- `tasks`: operation type/version, node ID, actor, status, priority, created/expiry, idempotency key, correlation ID, result reference.
- `task_attempts`: attempt number, dispatch/result timestamps, error category, sanitized output.
- `audit_events`: append-only event record, actor, target, action, result, timestamp, correlation ID, metadata with secret filtering.
- `settings`: typed key/value with schema and secret reference, not raw secret where possible.
- `integrations`: provider type, account metadata, encrypted secret reference, scope and status.
- `module_installations`: module ID/version, enabled state and required capability set.
- `backup_jobs`, `backup_snapshots`, `restore_jobs`: schedule, target, repository ref, verification status and state.

Use UUID/ULID identifiers consistently. Use UTC timestamps in storage. Index `node_id`, `status`, `created_at`, `correlation_id`; partition/high-volume audit/metrics only after measured need. Use migrations, not runtime auto-sync. Avoid polymorphic JSON for fields that require constraints or queries. Use JSONB only for versioned metadata.

Secret encryption: envelope encryption using an externally stored master key or OS key store. Record key version and rotation metadata. Do not treat PostgreSQL encryption at rest as a replacement for application-level secret handling.
