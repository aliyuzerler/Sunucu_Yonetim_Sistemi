# ADR-0004: PostgreSQL Source of Truth and Durable Job Queue

- Status: Accepted (baseline)
- Context: First deployment can be single Control Plane; adding Redis/NATS from day one increases complexity and licensing/operations burden.
- Decision: PostgreSQL stores durable entities, task state and transactional outbox. Use row locking/leases for initial workers. Add NATS JetStream only when measured event throughput or fan-out requires it. Valkey remains optional cache.
- Consequences: Clear transaction semantics and fewer moving parts initially; database worker throughput must be load-tested and can later be bridged to NATS.
