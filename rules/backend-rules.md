# Backend Rules

- Go with standard `net/http` patterns and context cancellation.
- Use chi for routing; keep handlers thin and call application services.
- Validate input at the boundary; domain service performs authorization and invariant checks.
- Use PostgreSQL transactions for state changes and outbox/task creation.
- Use explicit SQL migrations; avoid auto-migration in production.
- Return stable error codes; do not leak internal stack traces or secrets.
- Apply request size/time limits, rate limits and structured redaction.
