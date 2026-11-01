# ADR-0006: Multi-node Strategy — Single Control Plane First, Multi-node-ready Contracts

- Status: Accepted (baseline, Phase 00)
- Context: The first supported deployment is a single Control Plane instance, but the product goal is one control plane managing many nodes (local and remote). High availability and multi-instance Control Plane are future work. The acceptance criteria of Phase 00 require the multi-node strategy to be recorded in an ADR.

## Decision

1. **Initial deployment is one Control Plane instance.** HA, load-balanced or replicated Control Plane deployments are explicitly out of scope for initial releases and must not be promised in UI, docs or installer copy.
2. **Multi-node-ready contracts from day one.**
   - `nodes` are first-class rows with immutable IDs; every task, audit event, capability record and certificate binding is node-scoped.
   - The Agent protocol never assumes the Agent and Control Plane share a host or trust zone; agents are always remote peers over outbound mTLS (ADR-0002).
   - No single-node shortcuts in schemas, protobuf contracts or application services (no `node_id = NULL means local` semantics).
3. **Scale-out path (in order, only with measured need):**
   - PostgreSQL read/write tuning and worker partitioning using `FOR UPDATE SKIP LOCKED` leases (ADR-0004).
   - Optional NATS JetStream bridge for event fan-out when measured throughput requires it (ADR-0004).
   - Service split of the modular monolith only when scaling/reliability evidence justifies it (ADR-0001).
4. **Failure isolation.** One offline node must not block task dispatch, telemetry or UI for other nodes; per-node leases and deadlines (Durable Task Lifecycle document) are the mechanism.
5. **Security isolation.** Node identity, enrollment, rotation and revocation are per-node; a compromised node credential never grants access to other nodes or to the Control Plane database.

## Consequences

- Until HA work is approved through a new ADR, Control Plane downtime stops management operations (agents keep running locally; telemetry reconciles on reconnect).
- Multi-node-ready contracts cost a little up-front design discipline but avoid breaking schema/protocol migrations when scaling out.
- Capacity planning for one Control Plane (worker concurrency, DB load) must be documented per phase that adds background work.
