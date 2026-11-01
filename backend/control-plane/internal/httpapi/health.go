package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/platform"
)

// Pinger reports dependency health for the readiness endpoint. The database
// pool implements it; tests provide fakes.
type Pinger interface {
	Ping(ctx context.Context) error
}

// pingerFunc adapts a function to Pinger.
type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

// healthz reports process liveness. It must not touch dependencies.
func healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"request_id":  requestIDFrom(r.Context()),
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

// readyz reports readiness, including database reachability. It never leaks
// internal error strings to the client; details go to the server log.
func readyz(ping Pinger, onError func(*http.Request, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		err := ping.Ping(ctx)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{
				"status":      "ready",
				"components":  map[string]string{"database": "up"},
				"request_id":  requestIDFrom(r.Context()),
				"server_time": time.Now().UTC().Format(time.RFC3339),
			})
		case errors.Is(err, ErrNotConfigured):
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":      "degraded",
				"components":  map[string]string{"database": "not_configured"},
				"message":     "Veritabanı yapılandırılmamış; hazır değil.",
				"request_id":  requestIDFrom(r.Context()),
				"server_time": time.Now().UTC().Format(time.RFC3339),
			})
		default:
			if onError != nil {
				onError(r, err)
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":      "degraded",
				"components":  map[string]string{"database": "down"},
				"message":     "Veritabanı erişilemez; hazır değil.",
				"request_id":  requestIDFrom(r.Context()),
				"server_time": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
}

// version reports build metadata for smoke tests and support diagnostics.
func version(w http.ResponseWriter, r *http.Request) {
	info := platform.Info()
	info["request_id"] = requestIDFrom(r.Context())
	writeJSON(w, http.StatusOK, info)
}
