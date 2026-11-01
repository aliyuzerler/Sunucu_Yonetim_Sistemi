# ADR-0005: Linux-first Control Plane, Cross-platform Agent Contracts

- Status: Accepted (baseline)
- Context: Hosting, Linux service management, Docker and KVM are core first use cases; Windows has distinct service and security APIs.
- Decision: Control Plane initially runs on supported Linux. Agent domain contracts are OS-neutral, with Linux Agent first and Windows Agent in a dedicated later phase.
- Consequences: Shared protocol and UI are cross-platform; each OS-specific operation requires a native adapter and support-matrix tests. Do not imply Windows support before Phase 05 passes.
