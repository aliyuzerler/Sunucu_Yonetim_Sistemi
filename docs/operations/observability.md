# Observability

- Structured logs with UTC timestamps, severity, component, node ID, task ID and correlation ID.
- No credentials or private key material in logs.
- Prometheus-compatible metrics for Control Plane, Agent and modules.
- Metrics include freshness and collection errors; missing data must not be displayed as zero.
- Alert rules have severity, deduplication, silence and acknowledgement state.
- Audit events are distinct from debug/application logs and have retention/export policy.
- Bound cardinality: do not put user-supplied labels, arbitrary URL paths or secrets into metric labels.
