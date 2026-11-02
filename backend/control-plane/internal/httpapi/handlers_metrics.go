package httpapi

import (
	"crypto/subtle"
	"net/http"
	"time"
)

// handleMetrics exposes the Prometheus text exposition (Faz 13). The
// endpoint is NEVER session-authenticated (scrapers cannot log in); when
// CP_METRICS_TOKEN is configured a Bearer token is REQUIRED (fail-closed).
// Without a token the endpoint serves only bounded, secret-free aggregate
// series — deployment should still restrict exposure at the reverse proxy
// (documented in docs/architecture/monitoring.md).
// GET /metrics
func (a *api) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if a.metrics == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Metrik yüzeyi yapılandırılmamış.", false)
		return
	}
	if tok := a.cfg.MetricsToken; tok != "" {
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(got) <= len(prefix) || subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(tok)) != 1 {
			a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Geçerli Bearer token gerekli.", false)
			return
		}
	}
	body := a.metrics.Render(r.Context(), time.Now())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
