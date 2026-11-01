package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func pgService(t *testing.T) (*Service, *PGStore, *Registry) {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	store := NewPGStore(pool)
	reg, err := BuildDefaultRegistry(nopSetter{})
	if err != nil {
		t.Fatal(err)
	}
	return NewService(store, reg, nil), store, reg
}

type nopSetter struct{}

func (nopSetter) SetTags(_ context.Context, _, _, nodeID string, tags []string) (*inventory.Node, error) {
	return &inventory.Node{ID: nodeID, Tags: tags}, nil
}

// TestCreateSingleTransactionPG: task ve outbox kaydı tek transaction'da.
func TestCreateSingleTransactionPG(t *testing.T) {
	svc, store, _ := pgService(t)
	ctx := context.Background()

	tk, created, err := svc.Create(ctx, "admin", "corr-pg-1", CreateTaskInput{
		Operation:      "noop.echo",
		Payload:        json.RawMessage(`{"mesaj":"merhaba"}`),
		IdempotencyKey: "idem-pg-0001",
	})
	if err != nil || !created {
		t.Fatalf("oluşturma: created=%v err=%v", created, err)
	}

	// Task satırı.
	if got := testdb.QueryInt(t, store.pool,
		`SELECT count(*) FROM tasks WHERE id = $1::uuid AND status = 'queued'`, tk.ID); got != 1 {
		t.Fatalf("task satırı bekleniyordu: %d", got)
	}
	// Outbox satırı AYNI transaction'da yazılmış olmalı.
	if got := testdb.QueryInt(t, store.pool,
		`SELECT count(*) FROM outbox_events WHERE aggregate_id = $1::uuid AND event_type = 'task.created'`, tk.ID); got != 1 {
		t.Fatalf("outbox satırı bekleniyordu: %d", got)
	}

	// Idempotent replay.
	t2, created, err := svc.Create(ctx, "admin", "corr-pg-2", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{}`), IdempotencyKey: "idem-pg-0001",
	})
	if err != nil || created || t2.ID != tk.ID {
		t.Fatalf("idempotent replay: %v %v %v", created, err, t2 != nil && t2.ID != tk.ID)
	}

	// Doğrudan aynı key ile ikinci INSERT → benzersiz indeks → conflict.
	_, err = store.TaskByIDempotencyKey(ctx, "idem-pg-0001")
	if err != nil {
		t.Fatal(err)
	}
	dup := &Task{
		Operation: "noop.echo", OperationVersion: "1.0.0", Payload: json.RawMessage(`{}`),
		Status: StatusQueued, Actor: "admin", CorrelationID: "corr-dup", MaxAttempts: 3,
	}
	key := "idem-pg-0001"
	dup.IdempotencyKey = &key
	if err := store.CreateTaskWithOutbox(ctx, dup, OutboxEvent{EventType: "task.created"}); err != ErrIdempotencyConflict {
		t.Fatalf("benzersiz indeks conflict bekleniyordu: %v", err)
	}
}

// TestWorkerLeasePG: kiralama → çalıştırma → tamamlama uçtan uca.
func TestWorkerLeasePG(t *testing.T) {
	svc, store, reg := pgService(t)
	ctx := context.Background()

	const total = 5
	for i := 0; i < total; i++ {
		if _, _, err := svc.Create(ctx, "admin", fmt.Sprintf("corr-w-%d", i), CreateTaskInput{
			Operation: "noop.echo", Payload: json.RawMessage(`{"i":` + fmt.Sprint(i) + `}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	w := NewWorker(store, reg, nil, nil, nil, WorkerConfig{OwnerID: "pg-worker-1", Interval: 50 * time.Millisecond})
	for i := 0; i < total; i++ {
		w.claimAndExecute(ctx)
	}

	var done int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM tasks WHERE status = 'succeeded'`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if done != total {
		t.Fatalf("tüm görevler başarılı olmalı: %d/%d", done, total)
	}
	// Attempt satırları ve sonuç.
	var attempts, withResult int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE result IS NOT NULL) FROM tasks`).Scan(&attempts, &withResult); err != nil {
		t.Fatal(err)
	}
	if attempts != total || withResult != total {
		t.Fatalf("sonuç/attempt eksik: attempts=%d result=%d", attempts, withResult)
	}

	// Outbox: worker yayınladı → processed_at dolmalı.
	w.deliverOutbox(ctx)
	var pending int
	if err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE processed_at IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("outbox işlenmiş olmalı: %d bekliyor", pending)
	}
}

// TestWorkerConcurrentClaimPG: iki işçi eşzamanlı kiraladığında hiçbir task
// iki kez kiralık olmamalı (SKIP LOCKED kanıtı).
func TestWorkerConcurrentClaimPG(t *testing.T) {
	svc, store, reg := pgService(t)
	ctx := context.Background()

	const total = 12
	for i := 0; i < total; i++ {
		if _, _, err := svc.Create(ctx, "admin", fmt.Sprintf("corr-c-%d", i), CreateTaskInput{
			Operation: "noop.echo", Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 2
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = NewWorker(store, reg, nil, nil, nil, WorkerConfig{
				OwnerID: fmt.Sprintf("race-worker-%d", n), Interval: 10 * time.Millisecond,
			})
			// İşçiler doğrudan store claim döngüsünde yarışır (SKIP LOCKED kanıtı).
			for round := 0; round < 20; round++ {
				batch, err := store.ClaimBatch(ctx, fmt.Sprintf("race-worker-%d", n), 3, time.Minute)
				if err != nil {
					errCh <- err
					return
				}
				for _, tsk := range batch {
					attemptNo := tsk.Attempts + 1
					if err := store.StartAttempt(ctx, tsk.ID, attemptNo); err != nil {
						errCh <- err
						return
					}
					ok, err := store.CompleteExecution(ctx, tsk.ID, attemptNo, map[string]any{"ok": true})
					if err != nil {
						errCh <- err
						return
					}
					if !ok {
						errCh <- fmt.Errorf("task %s kiralanmıştı ama tamamlanamadı", tsk.ID)
						return
					}
				}
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	var succeeded, doubleAttempts int
	if err := store.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE status = 'succeeded'),
       count(*) FILTER (WHERE attempts > 1)
FROM tasks`).Scan(&succeeded, &doubleAttempts); err != nil {
		t.Fatal(err)
	}
	if succeeded != total {
		t.Fatalf("tüm görevler başarılı olmalı: %d/%d", succeeded, total)
	}
	if doubleAttempts != 0 {
		t.Fatalf("çift kiralama sızıntısı: %d görevde çift attempt", doubleAttempts)
	}
}

// TestLeaseRecoveryPG: lease kaybı — retry_safe requeue, değilse fail.
func TestLeaseRecoveryPG(t *testing.T) {
	_, store, _ := pgService(t)
	ctx := context.Background()

	mustInsert := func(id, op string, retrySafe bool) {
		testdb.MustExec(t, store.pool, `
INSERT INTO tasks (id, operation, operation_version, payload, status, actor, correlation_id, retry_safe, lease_owner, lease_expires_at)
VALUES ($1::uuid, $2, '1.0.0', '{}'::jsonb, 'dispatched', 'admin', 'corr-l', $3, 'dead-worker', now() - interval '2 minutes')`,
			id, op, retrySafe)
	}
	retryID := "aaaaaaaa-0000-0000-0000-000000000001"
	fatalID := "aaaaaaaa-0000-0000-0000-000000000002"
	mustInsert(retryID, "noop.echo", true)
	mustInsert(fatalID, "noop.echo", false)

	recovered, err := store.RecoverExpiredLeases(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 2 {
		t.Fatalf("iki task kurtarılmalı: %d", len(recovered))
	}
	var requeued, failed int
	for _, tsk := range recovered {
		switch tsk.ID {
		case retryID:
			if tsk.Status != StatusQueued {
				t.Fatalf("retry_safe task yeniden kuyruğa girmeli: %s", tsk.Status)
			}
			requeued++
		case fatalID:
			if tsk.Status != StatusFailed || tsk.ErrorCategory == nil || *tsk.ErrorCategory != ErrCatLeaseExpired {
				t.Fatalf("retry-dışı task lease_expired olmalı: %s %v", tsk.Status, tsk.ErrorCategory)
			}
			failed++
		}
	}
	if requeued != 1 || failed != 1 {
		t.Fatalf("kurtarma dağılımı: requeue=%d fail=%d", requeued, failed)
	}
}

// TestExpiryPG: deadline'ı geçen queued task expired olur.
func TestExpiryPG(t *testing.T) {
	_, store, _ := pgService(t)
	ctx := context.Background()

	testdb.MustExec(t, store.pool, `
INSERT INTO tasks (id, operation, operation_version, payload, status, actor, correlation_id, deadline_at)
VALUES ('bbbbbbbb-0000-0000-0000-000000000001', 'noop.echo', '1.0.0', '{}'::jsonb, 'queued', 'admin', 'corr-e', now() - interval '1 minute')`)

	expired, err := store.ExpireOverdueQueued(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Status != StatusExpired {
		t.Fatalf("task expired olmalı: %+v", expired)
	}
}

// TestCancelPG: queued iptal edilir; dispatch edilmiş iptal edilemez.
func TestCancelPG(t *testing.T) {
	svc, store, _ := pgService(t)
	ctx := context.Background()

	tk, _, err := svc.Create(ctx, "admin", "corr-x", CreateTaskInput{Operation: "noop.echo", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.CancelQueued(ctx, tk.ID)
	if err != nil || !ok {
		t.Fatalf("queued iptal: %v %v", ok, err)
	}
	testdb.MustExec(t, store.pool, `
INSERT INTO tasks (id, operation, operation_version, payload, status, actor, correlation_id)
VALUES ('cccccccc-0000-0000-0000-000000000001', 'noop.echo', '1.0.0', '{}'::jsonb, 'dispatched', 'admin', 'corr-y')`)
	ok, err = store.CancelQueued(ctx, "cccccccc-0000-0000-0000-000000000001")
	if err != nil || ok {
		t.Fatalf("dispatched iptal edilmemeli: %v %v", ok, err)
	}
}
