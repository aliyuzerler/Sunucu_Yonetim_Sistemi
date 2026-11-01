# ADR-0002: Outbound Agent Connections with mTLS

- Status: Accepted (baseline)
- Context: Managed nodes may be behind NAT/firewalls; inbound SSH/open management ports are undesirable.
- Decision: Each Agent initiates an outbound mTLS gRPC stream to Control Plane. Identity is certificate-bound to immutable node ID.
- Consequences: Requires enrollment, certificate lifecycle, revocation and reconnect logic. Control Plane must not rely on unauthenticated node claims.
