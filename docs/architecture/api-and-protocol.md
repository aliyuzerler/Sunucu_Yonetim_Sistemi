# API and Protocol Standards

## Browser API

- REST under `/api/v1`; OpenAPI document is source-controlled and validated in CI.
- JSON request/response envelopes with stable error codes.
- Pagination is cursor-based for event/task/log lists.
- Filtering/sorting fields explicitly allowlisted.
- Mutating requests use idempotency keys where duplicate execution could be harmful.
- Use ETags/version fields for optimistic concurrency when updating config.
- API versioning is independent from Agent gRPC protocol version.

## Agent protocol

- gRPC + Protocol Buffers over TLS 1.3/mTLS where platform support allows.
- Agent connects outbound and opens authenticated stream.
- Server dispatches only typed `OperationRequest` messages.
- Operation request includes target node, operation name/version, typed payload, deadline, idempotency key, correlation ID and policy reference.
- Agent returns accepted/running/progress/terminal result; Control Plane retains durable task state.
- Limit concurrent privileged operations per node; separate read-only telemetry channel from write actions where practical.

## Error envelope

Suggested fields: `code`, `message` (safe for UI), `details` (schema-validated and secret-free), `correlation_id`, `retryable`.

## Contract compatibility

- OpenAPI breaking-change check in CI.
- Protobuf field numbers never reused; deleted fields reserved.
- Unknown enum values handled safely.
- Agent/server compatibility matrix tested for current and minimum supported versions.
- Never silently reinterpret a newer operation as an older one.
