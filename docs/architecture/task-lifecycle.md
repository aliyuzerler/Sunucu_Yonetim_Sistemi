# Durable Task Lifecycle

## States

`queued → dispatched → accepted → running → succeeded | failed | partial | cancelled | expired | unknown`

A task can be `unknown` when the connection is lost and the result cannot yet be reconciled. The system must query actual state before retrying non-idempotent work.

## Reliability pattern

- Create task and outbox record in one PostgreSQL transaction.
- Worker claims rows with `FOR UPDATE SKIP LOCKED` and a lease.
- Dispatch to Agent; record attempt and deadline.
- Agent stores bounded idempotency results locally for a retention window.
- On reconnect, Agent reports pending/recent task outcomes.
- Retry only if operation is declared retry-safe and deadline remains.
- Use exponential backoff + jitter; cap attempts.
- Cancellation is cooperative; UI must show when an action cannot be safely interrupted.

## Operation classes

- Read-only: can generally retry.
- Idempotent desired-state: retry after state check.
- Non-idempotent: require operation-specific reconciliation; never blindly retry.
- Destructive: preflight + explicit confirmation + audit + rollback/backup policy.
