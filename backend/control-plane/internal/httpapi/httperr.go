// Package httpapi implements the browser-facing REST API of the control plane.
package httpapi

import (
	"encoding/json"
	"net/http"
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
