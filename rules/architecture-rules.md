# Architecture Rules

- Keep Control Plane modules independent behind interfaces and application services.
- No circular imports or cross-module database writes.
- Use domain IDs, not display names, for operations.
- Keep OS-specific logic in Agent adapters.
- Keep privileged operations in the local broker and expose only typed allowlisted commands.
- API schemas and Protobuf contracts are versioned and checked in.
- No direct UI-to-Agent or UI-to-host connections.
- Do not introduce a microservice, message broker, cache or framework without an ADR and measurable reason.
- All operations return structured status and correlation ID.
