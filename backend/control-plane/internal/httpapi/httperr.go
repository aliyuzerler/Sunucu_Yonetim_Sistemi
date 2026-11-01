package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/go-chi/chi/v5"
)

// Error envelope fields follow docs/architecture/api-and-protocol.md:
// code, message (safe for UI), details (schema-validated, secret-free),
// correlation_id, retryable.
type errorEnvelope struct {
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Details       map[string]any `json:"details,omitempty"`
	CorrelationID string         `json:"correlation_id"`
	Retryable     bool           `json:"retryable"`
}

// writeError emits the stable JSON error envelope.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Code:          code,
		Message:       message,
		CorrelationID: requestIDFrom(r.Context()),
		Retryable:     retryable,
	})
}

func (a *api) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	writeError(w, r, status, code, message, retryable)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// decodeJSON parses a bounded JSON body into dst.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// chiURLParam returns a URL path parameter.
func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// caller returns the authenticated actor name for audit records.
func caller(r *http.Request) string {
	if info, ok := authFrom(r.Context()); ok {
		return info.Username
	}
	return "anonymous"
}

// authEvent builds an audit event with the request correlation ID.
func auditEvent(info *authInfo, action, targetType, targetID, result, requestID string, meta map[string]any) audit.Event {
	actor := "anonymous"
	actorID := ""
	if info != nil {
		actor = info.Username
		actorID = info.UserID
	}
	return audit.Event{
		Actor:         actor,
		ActorID:       actorID,
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		Result:        result,
		CorrelationID: requestID,
		RequestID:     requestID,
		Metadata:      audit.Redact(meta).(map[string]any),
	}
}
