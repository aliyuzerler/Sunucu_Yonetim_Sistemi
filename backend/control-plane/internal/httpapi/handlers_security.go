package httpapi

import (
	"log/slog"
	"net/http"
)

// handleSecurityOverview renders the Faz 13 security posture report.
// GET /api/v1/security/overview
//
// The report contains ONLY provable controls; unknown evidence is returned
// as state "unknown" and is rendered as such in the UI (never safe/clean).
func (a *api) handleSecurityOverview(w http.ResponseWriter, r *http.Request) {
	if a.security == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Güvenlik merkezi yapılandırılmamış.", false)
		return
	}
	rep, err := a.security.Report(r.Context())
	if err != nil {
		a.logger.Error("security_overview_failed", slog.String("error", err.Error()), slog.String("request_id", requestIDFrom(r.Context())))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Güvenlik raporu üretilemedi.", false)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
