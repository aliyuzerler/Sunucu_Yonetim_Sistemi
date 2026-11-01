package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed task store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// uniqueViolation is the PostgreSQL constraint-violation SQLSTATE.
const uniqueViolation = "23505"

const taskCols = `id, operation, operation_version, node_id, payload, status,
                  execution_target, priority, actor, correlation_id, idempotency_key,
                  result, error_category, attempts, max_attempts, retry_safe,
                  deadline_at, lease_owner, lease_expires_at, created_at, updated_at`

func scanTask(row pgx.Row) (*Task, error) {
	t := &Task{}
	err := row.Scan(&t.ID, &t.Operation, &t.OperationVersion, &t.NodeID, &t.Payload,
		&t.Status, &t.ExecutionTarget, &t.Priority, &t.Actor, &t.CorrelationID,
		&t.IdempotencyKey, &t.Result, &t.ErrorCategory, &t.Attempts, &t.MaxAttempts,
		&t.RetrySafe, &t.DeadlineAt, &t.LeaseOwner, &t.LeaseExpiresAt, &t.CreatedAt,
		&t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// CreateTaskWithOutbox inserts the task and its outbox event in ONE
// transaction. A concurrent duplicate idempotency key maps to
// ErrIdempotencyConflict via the partial unique index.
func (s *PGStore) CreateTaskWithOutbox(ctx context.Context, t *Task, ev OutboxEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("task transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
INSERT INTO tasks (operation, operation_version, node_id, payload, status, execution_target,
                   priority, actor, correlation_id, idempotency_key, max_attempts, retry_safe, deadline_at)
VALUES ($1, $2, NULLIF($3,'')::uuid, $4, $5, $6, $7, $8, $9, NULLIF($10,''), $11, $12, $13)
RETURNING `+taskCols,
		t.Operation, t.OperationVersion, derefOrEmpty(t.NodeID), t.Payload, t.Status,
		t.ExecutionTarget, t.Priority, t.Actor, t.CorrelationID, derefOrEmpty(t.IdempotencyKey),
		t.MaxAttempts, t.RetrySafe, t.DeadlineAt,
	).Scan(&t.ID, &t.Operation, &t.OperationVersion, &t.NodeID, &t.Payload,
		&t.Status, &t.ExecutionTarget, &t.Priority, &t.Actor, &t.CorrelationID,
		&t.IdempotencyKey, &t.Result, &t.ErrorCategory, &t.Attempts, &t.MaxAttempts,
		&t.RetrySafe, &t.DeadlineAt, &t.LeaseOwner, &t.LeaseExpiresAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrIdempotencyConflict
		}
		return fmt.Errorf("task oluşturulamadı: %w", err)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4)`,
		ev.AggregateType, t.ID, ev.EventType,
		json.RawMessage(fmt.Sprintf(`{"operation":%q,"correlation_id":%q}`, t.Operation, t.CorrelationID)))
	if err != nil {
		return fmt.Errorf("outbox kaydı: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("task/outbox commit: %w", err)
	}
	ev.AggregateID = t.ID
	return nil
}

// TaskByIDempotencyKey resolves an existing task by key (nil if none).
func (s *PGStore) TaskByIDempotencyKey(ctx context.Context, key string) (*Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE idempotency_key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("idempotency sorgusu: %w", err)
	}
	return t, nil
}

// GetTask fetches one task.
func (s *PGStore) GetTask(ctx context.Context, id string) (*Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("task sorgusu: %w", err)
	}
	return t, nil
}

// GetAttempts returns attempts ordered by attempt number.
func (s *PGStore) GetAttempts(ctx context.Context, taskID string) ([]Attempt, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, task_id, attempt_no, status, error_category, output, started_at, finished_at
FROM task_attempts WHERE task_id = $1 ORDER BY attempt_no`, taskID)
	if err != nil {
		return nil, fmt.Errorf("attempt listesi: %w", err)
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.TaskID, &a.AttemptNo, &a.Status, &a.ErrorCategory,
			&a.Output, &a.StartedAt, &a.FinishedAt); err != nil {
			return nil, fmt.Errorf("attempt satırı: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListTasks applies allowlisted filters with keyset pagination.
func (s *PGStore) ListTasks(ctx context.Context, f ListFilter) (TaskPage, error) {
	where := []string{"true"}
	args := []any{}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.NodeID != "" {
		args = append(args, f.NodeID)
		where = append(where, fmt.Sprintf("node_id = $%d::uuid", len(args)))
	}
	if f.CorrelationID != "" {
		args = append(args, f.CorrelationID)
		where = append(where, fmt.Sprintf("correlation_id = $%d", len(args)))
	}
	if f.Cursor != "" {
		ts, id, err := decodeTaskCursor(f.Cursor)
		if err != nil {
			return TaskPage{}, fmt.Errorf("geçersiz imleç: %w", err)
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)

	q := `SELECT ` + taskCols + ` FROM tasks
          WHERE ` + strings.Join(where, " AND ") + `
          ORDER BY created_at DESC, id DESC
          LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return TaskPage{}, fmt.Errorf("task listesi: %w", err)
	}
	defer rows.Close()

	page := TaskPage{Tasks: []*Task{}}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return TaskPage{}, fmt.Errorf("task satırı: %w", err)
		}
		page.Tasks = append(page.Tasks, t)
	}
	if err := rows.Err(); err != nil {
		return TaskPage{}, fmt.Errorf("task listesi hatası: %w", err)
	}
	if len(page.Tasks) > f.Limit {
		page.HasMore = true
		page.Tasks = page.Tasks[:f.Limit]
		last := page.Tasks[len(page.Tasks)-1]
		page.NextCursor = encodeTaskCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// CancelQueued cancels a task that is still queued; returns false otherwise.
func (s *PGStore) CancelQueued(ctx context.Context, id string) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
UPDATE tasks SET status = 'cancelled', updated_at = now()
WHERE id = $1 AND status = 'queued'`, id)
	if err != nil {
		return false, fmt.Errorf("task iptali: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// CountTasks returns total task count.
func (s *PGStore) CountTasks(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tasks`).Scan(&n); err != nil {
		return 0, fmt.Errorf("task sayımı: %w", err)
	}
	return n, nil
}

// ClaimBatch atomically leases queued tasks: a single UPDATE over a
// FOR UPDATE SKIP LOCKED subquery guarantees no two workers share a task.
func (s *PGStore) ClaimBatch(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]*Task, error) {
	q := `UPDATE tasks SET
            status = 'dispatched',
            lease_owner = $1,
            lease_expires_at = now() + make_interval(secs => $2),
            updated_at = now()
          WHERE id IN (
            SELECT id FROM tasks
            WHERE status = 'queued'
              AND execution_target = 'control_plane'
              AND (deadline_at IS NULL OR deadline_at > now())
            ORDER BY priority DESC, created_at
            LIMIT $3
            FOR UPDATE SKIP LOCKED)
          RETURNING ` + taskCols

	rows, err := s.pool.Query(ctx, q, owner, leaseFor.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("task kiralaması: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("kiralama satırı: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// StartAttempt inserts the attempt row and bumps the task attempt counter.
func (s *PGStore) StartAttempt(ctx context.Context, taskID string, attemptNo int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("attempt transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO task_attempts (task_id, attempt_no, status) VALUES ($1, $2, 'started')`,
		taskID, attemptNo); err != nil {
		return fmt.Errorf("attempt kaydı: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tasks SET attempts = $2, updated_at = now() WHERE id = $1`,
		taskID, attemptNo); err != nil {
		return fmt.Errorf("attempt sayacı: %w", err)
	}
	return tx.Commit(ctx)
}

// CompleteExecution finishes a succeeded attempt if the lease still belongs
// to the caller's dispatch state; returns false when the lease was lost.
func (s *PGStore) CompleteExecution(ctx context.Context, taskID string, attemptNo int, result map[string]any) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("complete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `
UPDATE tasks SET status = 'succeeded', result = $2, lease_owner = NULL,
                 lease_expires_at = NULL, updated_at = now()
WHERE id = $1 AND status = 'dispatched'`, taskID, result)
	if err != nil {
		return false, fmt.Errorf("task tamamlama: %w", err)
	}
	if ct.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE task_attempts SET status = 'succeeded', finished_at = now()
WHERE task_id = $1 AND attempt_no = $2`, taskID, attemptNo); err != nil {
		return false, fmt.Errorf("attempt tamamlama: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("complete commit: %w", err)
	}
	return true, nil
}

// FailExecution finishes a failed attempt if the lease is still current.
func (s *PGStore) FailExecution(ctx context.Context, taskID string, attemptNo int, category, output string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("fail transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `
UPDATE tasks SET status = 'failed', error_category = $2, lease_owner = NULL,
                 lease_expires_at = NULL, updated_at = now()
WHERE id = $1 AND status = 'dispatched'`, taskID, category)
	if err != nil {
		return false, fmt.Errorf("task başarısızlık kaydı: %w", err)
	}
	if ct.RowsAffected() != 1 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
UPDATE task_attempts SET status = 'failed', error_category = $2, output = $3, finished_at = now()
WHERE task_id = $1 AND attempt_no = $4`, taskID, category, truncateOutput(output), attemptNo); err != nil {
		return false, fmt.Errorf("attempt başarısızlık kaydı: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("fail commit: %w", err)
	}
	return true, nil
}

// RecoverExpiredLeases requeues retry-safe tasks whose lease expired and
// fails the rest, atomically, using SKIP LOCKED to stay worker-safe.
func (s *PGStore) RecoverExpiredLeases(ctx context.Context, limit int) ([]*Task, error) {
	q := `UPDATE tasks SET
            status = CASE WHEN retry_safe AND attempts < max_attempts
                                AND (deadline_at IS NULL OR deadline_at > now())
                          THEN 'queued' ELSE 'failed' END,
            error_category = CASE WHEN retry_safe AND attempts < max_attempts
                                        AND (deadline_at IS NULL OR deadline_at > now())
                                  THEN NULL ELSE 'lease_expired' END,
            lease_owner = NULL,
            lease_expires_at = NULL,
            updated_at = now()
          WHERE id IN (
            SELECT id FROM tasks
            WHERE status = 'dispatched' AND lease_expires_at < now()
            LIMIT $1 FOR UPDATE SKIP LOCKED)
          RETURNING ` + taskCols

	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("lease kurtarma: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("kurtarma satırı: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ExpireOverdueQueued marks queued tasks past their deadline as expired.
func (s *PGStore) ExpireOverdueQueued(ctx context.Context, limit int) ([]*Task, error) {
	q := `UPDATE tasks SET status = 'expired', updated_at = now()
          WHERE id IN (
            SELECT id FROM tasks
            WHERE status = 'queued' AND deadline_at IS NOT NULL AND deadline_at < now()
            LIMIT $1 FOR UPDATE SKIP LOCKED)
          RETURNING ` + taskCols

	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("süre aşımı: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("süre aşımı satırı: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClaimOutboxBatch atomically claims unprocessed outbox events.
func (s *PGStore) ClaimOutboxBatch(ctx context.Context, limit int) ([]*OutboxEvent, error) {
	q := `UPDATE outbox_events SET attempts = attempts + 1
          WHERE id IN (
            SELECT id FROM outbox_events
            WHERE processed_at IS NULL AND next_attempt_at <= now()
            ORDER BY created_at
            LIMIT $1
            FOR UPDATE SKIP LOCKED)
          RETURNING id, aggregate_type, aggregate_id, event_type, payload, attempts`

	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox kiralaması: %w", err)
	}
	defer rows.Close()
	var out []*OutboxEvent
	for rows.Next() {
		ev := &OutboxEvent{}
		if err := rows.Scan(&ev.ID, &ev.AggregateType, &ev.AggregateID, &ev.EventType,
			&ev.Payload, &ev.Attempts); err != nil {
			return nil, fmt.Errorf("outbox satırı: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CompleteOutbox marks an event processed.
func (s *PGStore) CompleteOutbox(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE outbox_events SET processed_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("outbox tamamlama: %w", err)
	}
	return nil
}

// FailOutbox schedules the next delivery attempt with backoff.
func (s *PGStore) FailOutbox(ctx context.Context, id string, nextAt time.Time, lastErr string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE outbox_events SET next_attempt_at = $2, last_error = $3 WHERE id = $1`,
		id, nextAt, lastErr)
	if err != nil {
		return fmt.Errorf("outbox başarısızlık kaydı: %w", err)
	}
	return nil
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func encodeTaskCursor(ts time.Time, id string) string {
	return fmt.Sprintf("%d|%s", ts.UnixNano(), id)
}

func decodeTaskCursor(s string) (time.Time, string, error) {
	parts := strings.SplitN(s, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("cursor biçimi")
	}
	var ns int64
	if _, err := fmt.Sscanf(parts[0], "%d", &ns); err != nil {
		return time.Time{}, "", errors.New("cursor zaman")
	}
	return time.Unix(0, ns), parts[1], nil
}

// ClaimBatchForNode atomically leases queued node-targeted tasks for one
// agent stream (SKIP LOCKED; a node may have multiple streams over time but
// one active stream per process in Faz 03).
func (s *PGStore) ClaimBatchForNode(ctx context.Context, nodeID, owner string, limit int, leaseFor time.Duration) ([]*Task, error) {
	q := `UPDATE tasks SET
            status = 'dispatched',
            lease_owner = $1,
            lease_expires_at = now() + make_interval(secs => $2),
            updated_at = now()
          WHERE id IN (
            SELECT id FROM tasks
            WHERE status = 'queued'
              AND execution_target = 'node'
              AND node_id = $3::uuid
              AND (deadline_at IS NULL OR deadline_at > now())
            ORDER BY priority DESC, created_at
            LIMIT $4
            FOR UPDATE SKIP LOCKED)
          RETURNING ` + taskCols

	rows, err := s.pool.Query(ctx, q, owner, leaseFor.Seconds(), nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("node task kiralaması: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("node kiralama satırı: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
