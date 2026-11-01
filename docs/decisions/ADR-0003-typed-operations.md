# ADR-0003: Typed Allowlisted Operations, No Arbitrary Remote Shell

- Status: Accepted (security invariant)
- Context: A generic remote shell endpoint would turn a web compromise into host compromise.
- Decision: User actions map to versioned typed operations, validated by Control Plane, Agent and local broker. Arbitrary shell/PowerShell request payloads are prohibited.
- Consequences: More adapter code is required, but operations are auditable, testable and capability-aware. An interactive terminal, if ever added, must be a separate highly privileged feature with MFA, short-lived access and audit.
