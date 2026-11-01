package tasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// Worker leases queued tasks and executes them through the typed registry.
// Concurrency-safety comes from the store: claims use FOR UPDATE SKIP LOCKED
// and a lease, so multiple workers never execute the same task at once.
type Worker struct {
	store      Store
	registry   *Registry
	publisher  Publisher
	audit      audit.Recorder
	logger     *slog.Logger
	ownerID    string
	interval   time.Duration
	leaseFor   time.Duration
	claimLimit int
}

// WorkerConfig tunes the background worker.
type WorkerConfig struct {
	OwnerID    string        // unique per process instance
	Interval   time.Duration // polling interval, default 2s
	LeaseFor   time.Duration // lease duration, default 60s
	ClaimLimit int           // batch size, default 10
}

// NewWorker builds the worker.
func NewWorker(store Store, reg *Registry, pub Publisher, rec audit.Recorder, logger *slog.Logger, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.LeaseFor <= 0 {
		cfg.LeaseFor = 60 * time.Second
	}
	if cfg.ClaimLimit <= 0 {
		cfg.ClaimLimit = 10
	}
	if logger == nil {
		logger = slog.Default()
	}
	if pub == nil {
		pub = logPublisher{logger: logger}
	}
	return &Worker{
		store:      store,
		registry:   reg,
		publisher:  pub,
		audit:      rec,
		logger:     logger,
		ownerID:    cfg.OwnerID,
		interval:   cfg.Interval,
		leaseFor:   cfg.LeaseFor,
		claimLimit: cfg.ClaimLimit,
	}
}

// Run blocks until ctx is cancelled, executing one work cycle per interval.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("task_worker_started", slog.String("owner", w.ownerID))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		w.workOnce(ctx)
		select {
		case <-ctx.Done():
			w.logger.Info("task_worker_stopped", slog.String("owner", w.ownerID))
			return
		case <-ticker.C:
		}
	}
}

// workOnce performs a single maintenance + claim + execution + delivery cycle.
func (w *Worker) workOnce(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			w.logger.Error("task_worker_panic", slog.Any("panic", rec), slog.String("owner", w.ownerID))
		}
	}()

	w.recoverLeases(ctx)
	w.expireOverdue(ctx)
	w.claimAndExecute(ctx)
	w.deliverOutbox(ctx)
}

// recoverLeases requeues retry-safe tasks whose leases expired and fails the rest.
func (w *Worker) recoverLeases(ctx context.Context) {
	recovered, err := w.store.RecoverExpiredLeases(ctx, w.claimLimit)
	if err != nil {
		w.logger.Error("lease_recovery_failed", slog.String("error", err.Error()))
		return
	}
	for _, t := range recovered {
		switch t.Status {
		case StatusQueued:
			w.logger.Info("task_requeued",
				slog.String("task_id", t.ID), slog.Int("attempts", t.Attempts))
		case StatusFailed:
			w.logger.Warn("task_lease_lost_failed", slog.String("task_id", t.ID))
			w.auditEvent(ctx, t, "task.fail", audit.ResultFailure, map[string]any{
				"error_category": ErrCatLeaseExpired,
			})
		}
	}
}

// expireOverdue marks queued tasks past their deadline as expired.
func (w *Worker) expireOverdue(ctx context.Context) {
	expired, err := w.store.ExpireOverdueQueued(ctx, w.claimLimit)
	if err != nil {
		w.logger.Error("expire_sweep_failed", slog.String("error", err.Error()))
		return
	}
	for _, t := range expired {
		w.logger.Info("task_expired", slog.String("task_id", t.ID))
		w.auditEvent(ctx, t, "task.expire", audit.ResultFailure, nil)
	}
}

// claimAndExecute claims a lease batch and runs each task under its timeout.
func (w *Worker) claimAndExecute(ctx context.Context) {
	batch, err := w.store.ClaimBatch(ctx, w.ownerID, w.claimLimit, w.leaseFor)
	if err != nil {
		w.logger.Error("claim_failed", slog.String("error", err.Error()))
		return
	}
	for _, t := range batch {
		w.executeTask(ctx, t)
	}
}

// executeTask runs one claimed task exactly once under the lease.
func (w *Worker) executeTask(ctx context.Context, t *Task) {
	spec, ok := w.registry.Spec(t.Operation)
	if !ok {
		// Registry drift: fail the task deterministically.
		attemptNo := t.Attempts + 1
		if err := w.store.StartAttempt(ctx, t.ID, attemptNo); err != nil {
			w.logger.Error("attempt_start_failed",
				slog.String("task_id", t.ID), slog.String("error", err.Error()))
			return
		}
		w.fail(ctx, t, attemptNo, ErrCatUnsupported, "operasyon kayıtlı değil")
		return
	}

	attemptNo := t.Attempts + 1
	if err := w.store.StartAttempt(ctx, t.ID, attemptNo); err != nil {
		w.logger.Error("attempt_start_failed",
			slog.String("task_id", t.ID), slog.String("error", err.Error()))
		return
	}

	execCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	result, output, execErr := safeExecute(spec, execCtx, t)
	output = truncateOutput(output)

	if execErr != nil {
		category := ErrCatExecutor
		if errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execErr, context.Canceled) && ctx.Err() == nil {
			category = ErrCatTimeout
		}
		w.fail(ctx, t, attemptNo, category, output)
		return
	}

	okLease, err := w.store.CompleteExecution(ctx, t.ID, attemptNo, result)
	if err != nil {
		w.logger.Error("complete_failed",
			slog.String("task_id", t.ID), slog.String("error", err.Error()))
		return
	}
	if !okLease {
		// Lease lost while executing; the attempt result is discarded and the
		// recovery path owns the task now.
		w.logger.Warn("task_lease_lost_discard", slog.String("task_id", t.ID))
		return
	}
	w.logger.Info("task_succeeded",
		slog.String("task_id", t.ID),
		slog.String("operation", t.Operation),
		slog.String("correlation_id", t.CorrelationID))
	w.auditEvent(ctx, t, "task.complete", audit.ResultSuccess, nil)
}

// fail records a failed attempt and the terminal task state.
func (w *Worker) fail(ctx context.Context, t *Task, attemptNo int, category, output string) {
	okLease, err := w.store.FailExecution(ctx, t.ID, attemptNo, category, output)
	if err != nil {
		w.logger.Error("fail_execution_record_failed",
			slog.String("task_id", t.ID), slog.String("error", err.Error()))
		return
	}
	if !okLease {
		return
	}
	w.logger.Warn("task_failed",
		slog.String("task_id", t.ID),
		slog.String("category", category),
		slog.String("correlation_id", t.CorrelationID))
	w.auditEvent(ctx, t, "task.fail", audit.ResultFailure, map[string]any{"error_category": category})
}

// deliverOutbox publishes claimed outbox events with capped backoff on failure.
func (w *Worker) deliverOutbox(ctx context.Context) {
	events, err := w.store.ClaimOutboxBatch(ctx, w.claimLimit)
	if err != nil {
		w.logger.Error("outbox_claim_failed", slog.String("error", err.Error()))
		return
	}
	for _, ev := range events {
		pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := w.publisher.Publish(pubCtx, ev)
		cancel()
		switch {
		case err == nil:
			if err := w.store.CompleteOutbox(ctx, ev.ID); err != nil {
				w.logger.Error("outbox_complete_failed",
					slog.String("event_id", ev.ID), slog.String("error", err.Error()))
			}
		default:
			next := time.Now().UTC().Add(outboxBackoff(evAttempts(ev)))
			msg := truncateOutput(err.Error())
			if err := w.store.FailOutbox(ctx, ev.ID, next, msg); err != nil {
				w.logger.Error("outbox_fail_failed",
					slog.String("event_id", ev.ID), slog.String("error", err.Error()))
			} else {
				w.logger.Warn("outbox_delivery_retry",
					slog.String("event_id", ev.ID), slog.Time("next_attempt", next))
			}
		}
	}
}

func (w *Worker) auditEvent(ctx context.Context, t *Task, action, result string, meta map[string]any) {
	if w.audit == nil {
		return
	}
	_ = w.audit.Record(ctx, audit.Event{
		Actor:         "system",
		Action:        action,
		TargetType:    "task",
		TargetID:      t.ID,
		Result:        result,
		CorrelationID: t.CorrelationID,
		Metadata:      meta,
	})
}

// outboxBackoff: capped exponential backoff, 5s → 10m.
func outboxBackoff(attempts int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempts && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

// truncateOutput bounds executor output for storage (task_attempts.output).
func truncateOutput(s string) string {
	const max = 4000
	if len(s) > max {
		return s[:max]
	}
	return s
}

// safeExecute isolates panics inside executors.
func safeExecute(spec *OperationSpec, ctx context.Context, t *Task) (res map[string]any, output string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("executor panic: %v", rec)
		}
	}()
	return spec.Exec.Execute(ctx, t)
}

// logPublisher is the Faz 02 outbox publisher: structured log delivery. The
// gRPC agent delivery path replaces it in Faz 03+.
type logPublisher struct{ logger *slog.Logger }

func (p logPublisher) Publish(_ context.Context, ev *OutboxEvent) error {
	p.logger.Info("outbox_event",
		slog.String("event_id", ev.ID),
		slog.String("event_type", ev.EventType),
		slog.String("aggregate", ev.AggregateType),
		slog.String("aggregate_id", ev.AggregateID))
	return nil
}

// evAttempts normalizes the attempt counter for backoff computation.
func evAttempts(ev *OutboxEvent) int {
	if ev.Attempts <= 0 {
		return 1
	}
	return ev.Attempts
}
