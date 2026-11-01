package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
)

// ---- fakes ----

type fakeStore struct {
	tasks    map[string]*Task
	byIdem   map[string]string
	attempts map[string][]Attempt
	outbox   []*OutboxEvent
	seq      int
	failExec bool // simulate lease lost on completion
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tasks:    map[string]*Task{},
		byIdem:   map[string]string{},
		attempts: map[string][]Attempt{},
	}
}

func (f *fakeStore) CreateTaskWithOutbox(_ context.Context, t *Task, ev OutboxEvent) error {
	f.seq++
	t.ID = fmt.Sprintf("00000000-0000-0000-0000-%012d", f.seq)
	cp := *t
	f.tasks[t.ID] = &cp
	if t.IdempotencyKey != nil {
		f.byIdem[*t.IdempotencyKey] = t.ID
	}
	ev.ID = fmt.Sprintf("ev-%d", f.seq)
	ev.AggregateID = t.ID
	f.outbox = append(f.outbox, &ev)
	return nil
}

func (f *fakeStore) TaskByIDempotencyKey(_ context.Context, key string) (*Task, error) {
	if id, ok := f.byIdem[key]; ok {
		cp := *f.tasks[id]
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeStore) GetTask(_ context.Context, id string) (*Task, error) {
	if t, ok := f.tasks[id]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (f *fakeStore) GetAttempts(_ context.Context, taskID string) ([]Attempt, error) {
	return f.attempts[taskID], nil
}

func (f *fakeStore) ListTasks(context.Context, ListFilter) (TaskPage, error) {
	return TaskPage{Tasks: []*Task{}}, nil
}

func (f *fakeStore) CancelQueued(_ context.Context, id string) (bool, error) {
	t, ok := f.tasks[id]
	if !ok || t.Status != StatusQueued {
		return false, nil
	}
	t.Status = StatusCancelled
	return true, nil
}

func (f *fakeStore) CountTasks(context.Context) (int64, error) { return int64(len(f.tasks)), nil }

func (f *fakeStore) ClaimBatch(_ context.Context, owner string, limit int, leaseFor time.Duration) ([]*Task, error) {
	var out []*Task
	for _, t := range f.tasks {
		if len(out) >= limit {
			break
		}
		if t.Status != StatusQueued {
			continue
		}
		t.Status = StatusDispatched
		t.LeaseOwner = &owner
		exp := time.Now().Add(leaseFor)
		t.LeaseExpiresAt = &exp
		cp := *t
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeStore) StartAttempt(_ context.Context, taskID string, attemptNo int) error {
	f.attempts[taskID] = append(f.attempts[taskID], Attempt{TaskID: taskID, AttemptNo: attemptNo, Status: "started"})
	if t := f.tasks[taskID]; t != nil {
		t.Attempts = attemptNo
	}
	return nil
}

func (f *fakeStore) CompleteExecution(_ context.Context, taskID string, attemptNo int, result map[string]any) (bool, error) {
	if f.failExec {
		return false, nil
	}
	t := f.tasks[taskID]
	if t == nil || t.Status != StatusDispatched {
		return false, nil
	}
	t.Status = StatusSucceeded
	b, _ := json.Marshal(result)
	t.Result = b
	for i := range f.attempts[taskID] {
		if f.attempts[taskID][i].AttemptNo == attemptNo {
			f.attempts[taskID][i].Status = "succeeded"
		}
	}
	return true, nil
}

func (f *fakeStore) FailExecution(_ context.Context, taskID string, attemptNo int, category, output string) (bool, error) {
	t := f.tasks[taskID]
	if t == nil || t.Status != StatusDispatched {
		return false, nil
	}
	t.Status = StatusFailed
	cat := category
	t.ErrorCategory = &cat
	for i := range f.attempts[taskID] {
		if f.attempts[taskID][i].AttemptNo == attemptNo {
			f.attempts[taskID][i].Status = "failed"
		}
	}
	return true, nil
}

func (f *fakeStore) RecoverExpiredLeases(context.Context, int) ([]*Task, error) { return nil, nil }
func (f *fakeStore) ExpireOverdueQueued(context.Context, int) ([]*Task, error)  { return nil, nil }

func (f *fakeStore) ClaimOutboxBatch(context.Context, int) ([]*OutboxEvent, error) {
	out := f.outbox
	f.outbox = nil
	return out, nil
}

func (f *fakeStore) CompleteOutbox(_ context.Context, id string) error {
	for _, ev := range f.outbox {
		_ = ev
	}
	return nil
}

func (f *fakeStore) FailOutbox(context.Context, string, time.Time, string) error { return nil }

type spyAudit struct{ events []audit.Event }

func (s *spyAudit) Record(_ context.Context, e audit.Event) error {
	s.events = append(s.events, e)
	return nil
}

type fakeTagSetter struct {
	calls int
}

func (f *fakeTagSetter) SetTags(_ context.Context, _, _, nodeID string, tags []string) (*inventory.Node, error) {
	f.calls++
	return &inventory.Node{ID: nodeID, Tags: tags}, nil
}

type failingExecutor struct{}

func (failingExecutor) Execute(context.Context, *Task) (map[string]any, string, error) {
	return nil, "", errors.New("patladı")
}

type panickingExecutor struct{}

func (panickingExecutor) Execute(context.Context, *Task) (map[string]any, string, error) {
	panic("executor panic")
}

// ---- tests ----

func TestRegistryValidation(t *testing.T) {
	if _, err := NewRegistry(); err != nil {
		t.Fatalf("boş kayıt defteri kurulu olmalı: %v", err)
	}
	_, err := NewRegistry(&OperationSpec{
		Name: "BAD NAME", Version: "1", Class: ClassReadOnly,
		Timeout: time.Second, MaxPayload: 10, Validate: func(json.RawMessage) error { return nil },
		Exec: NoopExecutor{},
	})
	if err == nil {
		t.Fatal("geçersiz operasyon adı reddedilmeli")
	}
	reg, err := BuildDefaultRegistry(&fakeTagSetter{})
	if err != nil {
		t.Fatalf("varsayılan kayıt defteri: %v", err)
	}
	if len(reg.Names()) != 2 {
		t.Fatalf("iki operasyon bekleniyordu: %v", reg.Names())
	}
	if _, ok := reg.Spec("noop.echo"); !ok {
		t.Fatal("noop.echo kayıtlı olmalı")
	}
}

func TestPayloadValidators(t *testing.T) {
	if err := ValidateNoopPayload(json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("geçerli payload: %v", err)
	}
	if err := ValidateNoopPayload(json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("dizi payload reddedilmeli")
	}
	if err := ValidateNoopPayload(json.RawMessage(`not-json`)); err == nil {
		t.Fatal("bozuk JSON reddedilmeli")
	}
	valid := `{"node_id":"11111111-1111-1111-1111-111111111111","tags":["a","b"]}`
	if err := ValidateSetTagsPayload(json.RawMessage(valid)); err != nil {
		t.Fatalf("geçerli tag payload: %v", err)
	}
	if err := ValidateSetTagsPayload(json.RawMessage(`{"tags":["a"]}`)); err == nil {
		t.Fatal("node_id olmadan reddedilmeli")
	}
	if err := ValidateSetTagsPayload(json.RawMessage(`{"node_id":"x","tags":["a"],"ekstra":1}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
	if err := ValidateSetTagsPayload(json.RawMessage(`{"node_id":"x","tags":[]}`)); err == nil {
		t.Fatal("boş tags reddedilmeli")
	}
}

func newTestService(t *testing.T) (*Service, *fakeStore, *spyAudit) {
	t.Helper()
	store := newFakeStore()
	reg, err := BuildDefaultRegistry(&fakeTagSetter{})
	if err != nil {
		t.Fatal(err)
	}
	spy := &spyAudit{}
	return NewService(store, reg, spy), store, spy
}

func TestCreateValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	// Desteklenmeyen operasyon.
	if _, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{Operation: "shell.exec"}); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("desteklenmeyen operasyon reddedilmeli: %v", err)
	}
	// Geçersiz node.
	if _, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{
		Operation: "noop.echo", NodeID: "geçersiz",
		Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("geçersiz node reddedilmeli: %v", err)
	}
	// Geçersiz payload.
	if _, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`[1]`),
	}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("geçersiz payload reddedilmeli: %v", err)
	}
	// Payload limiti.
	if _, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{"blob":"` + strings.Repeat("x", 5000) + `"}`),
	}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("payload limiti aşılmalı: %v", err)
	}
	// Geçersiz correlation.
	if _, _, err := svc.Create(ctx, "admin", "!!", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, ErrInvalidCorrelation) {
		t.Fatalf("geçersiz correlation reddedilmeli: %v", err)
	}
	// Geçersiz idempotency key.
	if _, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{}`), IdempotencyKey: "kısa",
	}); !errors.Is(err, ErrInvalidIdemKey) {
		t.Fatalf("kısa idempotency key reddedilmeli: %v", err)
	}
}

func TestCreateSuccessAndIdempotency(t *testing.T) {
	svc, store, spy := newTestService(t)
	ctx := context.Background()

	t1, created, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{"selam":"dünya"}`),
		IdempotencyKey: "idempotent-1",
	})
	if err != nil || !created {
		t.Fatalf("ilk oluşturma: created=%v err=%v", created, err)
	}
	if t1.Status != StatusQueued {
		t.Fatalf("başlangıç durumu queued: %s", t1.Status)
	}
	if len(store.outbox) != 1 {
		t.Fatalf("outbox event yazılmalı: %d", len(store.outbox))
	}
	if store.outbox[0].AggregateID != t1.ID || store.outbox[0].EventType != "task.created" {
		t.Fatalf("outbox bağlantısı hatalı: %+v", store.outbox[0])
	}

	// Aynı key + aynı operasyon → mevcut task döner, created=false.
	t2, created, err := svc.Create(ctx, "admin", "c-2", CreateTaskInput{
		Operation: "noop.echo", Payload: json.RawMessage(`{"selam":"dünya"}`),
		IdempotencyKey: "idempotent-1",
	})
	if err != nil || created {
		t.Fatalf("idempotent tekrar: created=%v err=%v", created, err)
	}
	if t2.ID != t1.ID {
		t.Fatalf("aynı task dönmeli: %s != %s", t1.ID, t2.ID)
	}

	// Aynı key + farklı operasyon → conflict.
	if _, _, err := svc.Create(ctx, "admin", "c-3", CreateTaskInput{
		Operation:      "inventory.node.tag.set",
		Payload:        json.RawMessage(`{"node_id":"11111111-1111-1111-1111-111111111111","tags":["a"]}`),
		IdempotencyKey: "idempotent-1",
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency çatışması bekleniyordu: %v", err)
	}
	if len(spy.events) == 0 || spy.events[0].Action != "task.create" {
		t.Fatal("task.create audit bekleniyordu")
	}
}

func TestCreateTagSetRequiresNode(t *testing.T) {
	svc, _, _ := newTestService(t)
	// payload node_id içeriyor; node_id alanı boş olabilir (payload'dan gelir).
	tk, created, err := svc.Create(context.Background(), "admin", "c-1", CreateTaskInput{
		Operation: "inventory.node.tag.set",
		Payload:   json.RawMessage(`{"node_id":"11111111-1111-1111-1111-111111111111","tags":["prod"]}`),
	})
	if err != nil || !created {
		t.Fatalf("tag task oluşturulamadı: %v", err)
	}
	if tk.Operation != "inventory.node.tag.set" {
		t.Fatal("operasyon korunmalı")
	}
}

func TestCancel(t *testing.T) {
	svc, store, spy := newTestService(t)
	ctx := context.Background()
	t1, _, err := svc.Create(ctx, "admin", "c-1", CreateTaskInput{Operation: "noop.echo", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Cancel(ctx, "admin", "c-1", t1.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("queued task iptal edilmeli: %+v %v", got, err)
	}
	// İkinci kez: zaten iptal.
	if _, err := svc.Cancel(ctx, "admin", "c-1", t1.ID); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("tekrar iptal reddedilmeli: %v", err)
	}
	// Geçersiz UUID.
	if _, err := svc.Cancel(ctx, "admin", "c-1", "x"); !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("geçersiz id reddedilmeli: %v", err)
	}
	// Dispatch edilmiş task iptal edilemez.
	t2, _, _ := svc.Create(ctx, "admin", "c-2", CreateTaskInput{Operation: "noop.echo", Payload: json.RawMessage(`{}`)})
	store.tasks[t2.ID].Status = StatusDispatched
	if _, err := svc.Cancel(ctx, "admin", "c-2", t2.ID); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("dispatched task iptal edilememeli: %v", err)
	}
	found := false
	for _, e := range spy.events {
		if e.Action == "task.cancel" {
			found = true
		}
	}
	if !found {
		t.Fatal("task.cancel audit bekleniyordu")
	}
}

func runWorker(t *testing.T, store *fakeStore, reg *Registry) (*Worker, *spyAudit, *collectPublisher) {
	t.Helper()
	spy := &spyAudit{}
	pub := &collectPublisher{}
	return NewWorker(store, reg, pub, spy, nil, WorkerConfig{OwnerID: "w1"}), spy, pub
}

type collectPublisher struct{ events []*OutboxEvent }

func (c *collectPublisher) Publish(_ context.Context, ev *OutboxEvent) error {
	c.events = append(c.events, ev)
	return nil
}

func TestWorkerExecutesQueuedTask(t *testing.T) {
	svc, store, _ := newTestService(t)
	reg, _ := BuildDefaultRegistry(&fakeTagSetter{})
	w, spy, pub := runWorker(t, store, reg)
	ctx := context.Background()

	t1, _, _ := svc.Create(ctx, "admin", "c-1", CreateTaskInput{Operation: "noop.echo", Payload: json.RawMessage(`{"a":1}`)})
	w.claimAndExecute(ctx)

	got := store.tasks[t1.ID]
	if got.Status != StatusSucceeded {
		t.Fatalf("task başarılı olmalı: %+v", got)
	}
	if len(store.attempts[t1.ID]) != 1 || store.attempts[t1.ID][0].Status != "succeeded" {
		t.Fatalf("attempt kaydı: %+v", store.attempts[t1.ID])
	}
	w.deliverOutbox(ctx)
	if len(pub.events) != 1 {
		t.Fatalf("outbox yayınlamalı: %d", len(pub.events))
	}
	found := false
	for _, e := range spy.events {
		if e.Action == "task.complete" && e.CorrelationID == "c-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("task.complete audit bekleniyordu")
	}
}

func TestWorkerFailsOnExecutorError(t *testing.T) {
	store := newFakeStore()
	reg, err := NewRegistry(&OperationSpec{
		Name: "boom.op", Version: "1.0.0", Class: ClassNonIdem, RetrySafe: false,
		Timeout: time.Second, MaxPayload: 1024,
		Validate: func(json.RawMessage) error { return nil },
		Exec:     failingExecutor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, spy, _ := runWorker(t, store, reg)
	ctx := context.Background()

	store.CreateTaskWithOutbox(ctx, &Task{
		Operation: "boom.op", OperationVersion: "1.0.0",
		Payload: json.RawMessage(`{}`), Status: StatusQueued,
		Actor: "admin", CorrelationID: "c-9", MaxAttempts: 3,
	}, OutboxEvent{AggregateType: "task", EventType: "task.created"})

	ids := []string{}
	for id := range store.tasks {
		ids = append(ids, id)
	}
	w.claimAndExecute(ctx)
	got := store.tasks[ids[0]]
	if got.Status != StatusFailed || got.ErrorCategory == nil || *got.ErrorCategory != ErrCatExecutor {
		t.Fatalf("executor hatası kaydedilmeli: %+v", got)
	}
	found := false
	for _, e := range spy.events {
		if e.Action == "task.fail" {
			found = true
		}
	}
	if !found {
		t.Fatal("task.fail audit bekleniyordu")
	}
}

func TestWorkerFailsOnExecutorPanic(t *testing.T) {
	store := newFakeStore()
	reg, _ := NewRegistry(&OperationSpec{
		Name: "panic.op", Version: "1.0.0", Class: ClassReadOnly, RetrySafe: true,
		Timeout: time.Second, MaxPayload: 1024,
		Validate: func(json.RawMessage) error { return nil },
		Exec:     panickingExecutor{},
	})
	w, _, _ := runWorker(t, store, reg)
	ctx := context.Background()

	store.CreateTaskWithOutbox(ctx, &Task{
		Operation: "panic.op", OperationVersion: "1.0.0",
		Payload: json.RawMessage(`{}`), Status: StatusQueued,
		Actor: "admin", CorrelationID: "c-8", MaxAttempts: 3,
	}, OutboxEvent{EventType: "task.created"})

	w.claimAndExecute(ctx)
	for _, got := range store.tasks {
		if got.Status != StatusFailed {
			t.Fatalf("panic yakalanıp failed olmalı: %+v", got)
		}
	}
}

func TestWorkerUnsupportedOperation(t *testing.T) {
	store := newFakeStore()
	reg, _ := BuildDefaultRegistry(&fakeTagSetter{})
	w, _, _ := runWorker(t, store, reg)
	ctx := context.Background()

	store.CreateTaskWithOutbox(ctx, &Task{
		Operation: "ghost.op", OperationVersion: "1.0.0",
		Payload: json.RawMessage(`{}`), Status: StatusQueued,
		Actor: "system", CorrelationID: "c-7", MaxAttempts: 3,
	}, OutboxEvent{EventType: "task.created"})
	w.claimAndExecute(ctx)
	for _, got := range store.tasks {
		if got.Status != StatusFailed || got.ErrorCategory == nil || *got.ErrorCategory != ErrCatUnsupported {
			t.Fatalf("desteklenmeyen operasyon failed olmalı: %+v", got)
		}
	}
}

func TestOutboxBackoffCaps(t *testing.T) {
	if outboxBackoff(0) != 5*time.Second {
		t.Fatal("ilk deneme 5s")
	}
	if outboxBackoff(1) != 5*time.Second {
		t.Fatal("ilk deneme 5s")
	}
	if outboxBackoff(5) < 30*time.Second {
		t.Fatal("5. denemede üstel artış")
	}
	if outboxBackoff(20) != 10*time.Minute {
		t.Fatal("üst sınır 10dk")
	}
	if outboxBackoff(-3) != 5*time.Second {
		t.Fatal("negatif deneme 5s")
	}
}

func TestTruncateOutput(t *testing.T) {
	if got := truncateOutput(strings.Repeat("x", 5000)); len(got) != 4000 {
		t.Fatalf("çıktı 4000 karaktere sınırlandırılmalı: %d", len(got))
	}
}
