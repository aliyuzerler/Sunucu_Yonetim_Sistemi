# ADR-0001: Modular Monolith Control Plane

- Status: Accepted (baseline)
- Context: Many infrastructure modules are needed, but early microservices increase operational complexity.
- Decision: Build the Control Plane as a Go modular monolith with clear internal module boundaries; deploy Agents separately.
- Consequences: Faster initial development and simpler transactions; modules must respect interfaces to avoid a tangled monolith. Split only when measured scaling/reliability needs justify it.
