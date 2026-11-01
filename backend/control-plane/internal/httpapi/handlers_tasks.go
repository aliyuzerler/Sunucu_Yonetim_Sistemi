package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
)

// parseIntDefault parses a query integer with a fallback.
func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// handleListOperations returns the typed operation registry metadata.
// GET /api/v1/task-operations
func (a *api) handleListOperations(w http.ResponseWriter, r *http.Request) {
	if a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Görev motoru yapılandırılmamış.", false)
		return
	}
	type opInfo struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Class     string `json:"class"`
		RetrySafe bool   `json:"retry_safe"`
		TimeoutS  int    `json:"timeout_seconds"`
	}
	ops := []opInfo{}
	for _, name := range a.tasks.OperationNames() {
		spec, ok := a.tasks.OperationSpec(name)
		if !ok {
			continue
		}
		ops = append(ops, opInfo{
			Name:      spec.Name,
			Version:   spec.Version,
			Class:     string(spec.Class),
			RetrySafe: spec.RetrySafe,
			TimeoutS:  int(spec.Timeout / time.Second),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
}

// handleListTasks lists tasks with allowlisted filters.
// GET /api/v1/tasks?status=&node_id=&correlation_id=&limit=&cursor=
func (a *api) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Görev motoru yapılandırılmamış.", false)
		return
	}
	q := r.URL.Query()
	page, err := a.tasks.List(r.Context(), tasks.ListFilter{
		Status:        q.Get("status"),
		NodeID:        q.Get("node_id"),
		CorrelationID: q.Get("correlation_id"),
		Limit:         parseIntDefault(q.Get("limit"), 50),
		Cursor:        q.Get("cursor"),
	})
	if err != nil {
		a.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGetTask returns one task with attempts.
// GET /api/v1/tasks/{taskID}
func (a *api) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Görev motoru yapılandırılmamış.", false)
		return
	}
	t, attempts, err := a.tasks.Get(r.Context(), chiURLParam(r, "taskID"))
	if err != nil {
		a.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "attempts": attempts})
}

// handleCreateTask creates a durable task + outbox record in one transaction.
// POST /api/v1/tasks
func (a *api) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Görev motoru yapılandırılmamış.", false)
		return
	}
	var req struct {
		Operation      string          `json:"operation"`
		NodeID         string          `json:"node_id"`
		Payload        json.RawMessage `json:"payload"`
		IdempotencyKey string          `json:"idempotency_key"`
		DeadlineInS    int             `json:"deadline_seconds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	var deadlineIn time.Duration
	if req.DeadlineInS > 0 {
		deadlineIn = time.Duration(req.DeadlineInS) * time.Second
		if deadlineIn > 24*time.Hour {
			deadlineIn = 24 * time.Hour
		}
	}
	t, created, err := a.tasks.Create(r.Context(), caller(r), requestIDFrom(r.Context()), tasks.CreateTaskInput{
		Operation:      req.Operation,
		NodeID:         req.NodeID,
		Payload:        req.Payload,
		IdempotencyKey: req.IdempotencyKey,
		DeadlineIn:     deadlineIn,
	})
	if err != nil {
		a.writeTaskError(w, r, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK // idempotent replay
	}
	writeJSON(w, status, t)
}

// handleCancelTask cooperatively cancels a queued task.
// POST /api/v1/tasks/{taskID}/cancel
func (a *api) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	if a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Görev motoru yapılandırılmamış.", false)
		return
	}
	t, err := a.tasks.Cancel(r.Context(), caller(r), requestIDFrom(r.Context()), chiURLParam(r, "taskID"))
	if err != nil {
		a.writeTaskError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// writeTaskError maps task domain errors to API responses.
func (a *api) writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound), errors.Is(err, tasks.ErrInvalidNodeID):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Kayıt bulunamadı.", false)
	case errors.Is(err, tasks.ErrUnsupportedOperation):
		a.writeError(w, r, http.StatusBadRequest, "unsupported_operation", err.Error(), false)
	case errors.Is(err, tasks.ErrInvalidPayload),
		errors.Is(err, tasks.ErrInvalidCorrelation),
		errors.Is(err, tasks.ErrInvalidIdemKey):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, tasks.ErrIdempotencyConflict):
		a.writeError(w, r, http.StatusConflict, "idempotency_conflict", err.Error(), false)
	case errors.Is(err, tasks.ErrNotCancellable):
		a.writeError(w, r, http.StatusConflict, "not_cancellable", err.Error(), false)
	default:
		a.logger.Error("task_error",
			"error", err.Error(),
			"request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Beklenmeyen sunucu hatası.", false)
	}
}
