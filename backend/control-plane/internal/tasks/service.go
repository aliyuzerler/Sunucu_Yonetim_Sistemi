package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// Task statuses (docs/architecture/task-lifecycle.md). The Faz 02 internal
// worker uses queued/dispatched/succeeded/failed/cancelled/expired; the rest
// are reserved for agent-dispatched operations (Faz 03+).
const (
	StatusQueued     = "queued"
	StatusDispatched = "dispatched"
	StatusAccepted   = "accepted"
	StatusRunning    = "running"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
	StatusExpired    = "expired"
	StatusUnknown    = "unknown"
	StatusPartial    = "partial"
)

// Error categories for attempts/terminal failures.
const (
	ErrCatPayload      = "payload_invalid"
	ErrCatUnsupported  = "unsupported_operation"
	ErrCatExecutor     = "executor_error"
	ErrCatTimeout      = "timeout"
	ErrCatLeaseExpired = "lease_expired"
	ErrCatCancelled    = "cancelled"
)

var (
	uuidRE    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	correlRE  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	idemKeyRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)
)

// Validation errors mapped by handlers.
var (
	ErrInvalidPayload     = errors.New("payload doğrulanamadı")
	ErrInvalidNodeID      = errors.New("node_id UUID olmalı")
	ErrInvalidCorrelation = errors.New("correlation ID biçimi geçersiz")
	ErrInvalidIdemKey     = errors.New("idempotency key 8..128 karakter olmalı")
	ErrNotFound           = errors.New("görev bulunamadı")
	ErrNotCancellable     = errors.New("görev iptal edilemez durumda")
)

// Task is the durable unit of work.
type Task struct {
	ID               string          `json:"id"`
	Operation        string          `json:"operation"`
	OperationVersion string          `json:"operation_version"`
	NodeID           *string         `json:"node_id,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	Status           string          `json:"status"`
	Priority         int             `json:"priority"`
	Actor            string          `json:"actor"`
	CorrelationID    string          `json:"correlation_id"`
	IdempotencyKey   *string         `json:"idempotency_key,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	ErrorCategory    *string         `json:"error_category,omitempty"`
	Attempts         int             `json:"attempts"`
	MaxAttempts      int             `json:"max_attempts"`
	RetrySafe        bool            `json:"retry_safe"`
	DeadlineAt       *time.Time      `json:"deadline_at,omitempty"`
	LeaseOwner       *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt   *time.Time      `json:"lease_expires_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Attempt is one bounded execution record.
type Attempt struct {
	ID            string     `json:"id"`
	TaskID        string     `json:"task_id"`
	AttemptNo     int        `json:"attempt_no"`
	Status        string     `json:"status"`
	ErrorCategory *string    `json:"error_category,omitempty"`
	Output        *string    `json:"output,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// OutboxEvent is written in the SAME transaction as task creation.
type OutboxEvent struct {
	ID            string          `json:"id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	Attempts      int             `json:"attempts"`
}

// CreateTaskInput is the validated creation request.
type CreateTaskInput struct {
	Operation      string
	NodeID         string // optional; empty for node-less internal ops
	Payload        json.RawMessage
	IdempotencyKey string
	Priority       int
	DeadlineIn     time.Duration // 0 → no deadline
}

// Store is the persistence contract.
type Store interface {
	// CreateTaskWithOutbox inserts task and outbox event in ONE transaction
	// (phase-02 acceptance criterion).
	CreateTaskWithOutbox(ctx context.Context, t *Task, ev OutboxEvent) error
	TaskByIDempotencyKey(ctx context.Context, key string) (*Task, error)
	GetTask(ctx context.Context, id string) (*Task, error)
	GetAttempts(ctx context.Context, taskID string) ([]Attempt, error)
	ListTasks(ctx context.Context, f ListFilter) (TaskPage, error)
	CancelQueued(ctx context.Context, id string) (bool, error)
	CountTasks(ctx context.Context) (int64, error)

	// Worker operations (lease + SKIP LOCKED in SQL store).
	ClaimBatch(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]*Task, error)
	StartAttempt(ctx context.Context, taskID string, attemptNo int) error
	CompleteExecution(ctx context.Context, taskID string, attemptNo int, result map[string]any) (bool, error)
	FailExecution(ctx context.Context, taskID string, attemptNo int, category, output string) (bool, error)
	RecoverExpiredLeases(ctx context.Context, limit int) ([]*Task, error)
	ExpireOverdueQueued(ctx context.Context, limit int) ([]*Task, error)
	ClaimOutboxBatch(ctx context.Context, limit int) ([]*OutboxEvent, error)
	CompleteOutbox(ctx context.Context, id string) error
	FailOutbox(ctx context.Context, id string, nextAt time.Time, lastErr string) error
}

// Publisher delivers outbox events (structured-log publisher in Faz 02;
// the agent gRPC delivery path arrives in Faz 03+).
type Publisher interface {
	Publish(ctx context.Context, ev *OutboxEvent) error
}

// Service implements task use cases.
type Service struct {
	store    Store
	registry *Registry
	audit    audit.Recorder
}

// NewService builds the task service.
func NewService(store Store, reg *Registry, rec audit.Recorder) *Service {
	return &Service{store: store, registry: reg, audit: rec}
}

// Create validates the input against the typed operation registry and creates
// the task together with its outbox event in a single transaction.
func (s *Service) Create(ctx context.Context, actor, correlationID string, in CreateTaskInput) (*Task, bool, error) {
	spec, ok := s.registry.Spec(in.Operation)
	if !ok {
		return nil, false, fmt.Errorf("%w: %s (desteklenenler: %s)",
			ErrUnsupportedOperation, in.Operation, strings.Join(s.registry.Names(), ", "))
	}
	if in.NodeID != "" && !uuidRE.MatchString(in.NodeID) {
		return nil, false, ErrInvalidNodeID
	}
	if !correlRE.MatchString(correlationID) {
		return nil, false, ErrInvalidCorrelation
	}
	if len(in.Payload) > spec.MaxPayload {
		return nil, false, fmt.Errorf("%w: limit %d bayt", ErrInvalidPayload, spec.MaxPayload)
	}
	if len(strings.TrimSpace(string(in.Payload))) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if err := spec.Validate(in.Payload); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}

	idem := strings.TrimSpace(in.IdempotencyKey)
	if idem != "" {
		if !idemKeyRE.MatchString(idem) {
			return nil, false, ErrInvalidIdemKey
		}
		existing, err := s.store.TaskByIDempotencyKey(ctx, idem)
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			if existing.Operation != in.Operation {
				return nil, false, ErrIdempotencyConflict
			}
			// Idempotent replay: return the original task (created=false).
			return existing, false, nil
		}
	}

	var nodeID *string
	if in.NodeID != "" {
		n := in.NodeID
		nodeID = &n
	}
	var deadline *time.Time
	if in.DeadlineIn > 0 {
		d := time.Now().UTC().Add(in.DeadlineIn)
		deadline = &d
	}

	t := &Task{
		Operation:        spec.Name,
		OperationVersion: spec.Version,
		NodeID:           nodeID,
		Payload:          in.Payload,
		Status:           StatusQueued,
		Priority:         clampPriority(in.Priority),
		Actor:            actor,
		CorrelationID:    correlationID,
		RetrySafe:        spec.RetrySafe,
		MaxAttempts:      3,
		DeadlineAt:       deadline,
	}
	if idem != "" {
		k := idem
		t.IdempotencyKey = &k
	}

	ev := OutboxEvent{
		AggregateType: "task",
		AggregateID:   t.ID, // filled by store after ID generation
		EventType:     "task.created",
	}
	if err := s.store.CreateTaskWithOutbox(ctx, t, ev); err != nil {
		return nil, false, err
	}

	s.record(ctx, actor, correlationID, "task.create", "task", t.ID, audit.ResultSuccess,
		map[string]any{"operation": t.Operation, "node_id": in.NodeID, "idempotency_key": idem})
	return t, true, nil
}

// Get returns a task with its attempts.
func (s *Service) Get(ctx context.Context, id string) (*Task, []Attempt, error) {
	if !uuidRE.MatchString(id) {
		return nil, nil, ErrInvalidNodeID
	}
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	att, err := s.store.GetAttempts(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if att == nil {
		att = []Attempt{}
	}
	return t, att, nil
}

// ListFilter is allowlisted listing with keyset cursor.
type ListFilter struct {
	Status        string
	NodeID        string
	CorrelationID string
	Limit         int
	Cursor        string
}

// TaskPage is one page of tasks.
type TaskPage struct {
	Tasks      []*Task `json:"tasks"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

// ValidStatuses lists states the API may filter on.
func ValidStatuses() []string {
	return []string{StatusQueued, StatusDispatched, StatusAccepted, StatusRunning,
		StatusSucceeded, StatusFailed, StatusCancelled, StatusExpired, StatusUnknown, StatusPartial}
}

// List returns tasks with allowlisted filters.
func (s *Service) List(ctx context.Context, f ListFilter) (TaskPage, error) {
	if f.Status != "" && !contains(ValidStatuses(), f.Status) {
		return TaskPage{}, fmt.Errorf("geçersiz durum filtresi: %s", f.Status)
	}
	if f.NodeID != "" && !uuidRE.MatchString(f.NodeID) {
		return TaskPage{}, ErrInvalidNodeID
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return s.store.ListTasks(ctx, f)
}

// Cancel cooperatively cancels a queued task. Dispatched tasks cannot be
// cancelled from the API (their lease may already be executing).
func (s *Service) Cancel(ctx context.Context, actor, correlationID, id string) (*Task, error) {
	if !uuidRE.MatchString(id) {
		return nil, ErrInvalidNodeID
	}
	ok, err := s.store.CancelQueued(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotCancellable
	}
	s.record(ctx, actor, correlationID, "task.cancel", "task", id, audit.ResultSuccess, nil)
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// CountTasks returns the total task count (overview card).
func (s *Service) CountTasks(ctx context.Context) (int64, error) {
	return s.store.CountTasks(ctx)
}

func clampPriority(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Service) record(ctx context.Context, actor, correlationID, action, targetType, targetID, result string, meta map[string]any) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, audit.Event{
		Actor:         actor,
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		Result:        result,
		CorrelationID: correlationID,
		Metadata:      audit.Redact(meta).(map[string]any),
	})
}

// OperationNames returns the sorted names of the registered operations.
func (s *Service) OperationNames() []string { return s.registry.Names() }

// OperationSpec exposes one spec for API metadata.
func (s *Service) OperationSpec(name string) (*OperationSpec, bool) {
	return s.registry.Spec(name)
}
