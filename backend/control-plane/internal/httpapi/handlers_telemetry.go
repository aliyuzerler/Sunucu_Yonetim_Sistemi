package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
)

// handleGetNodeSystemFacts returns the agent-reported platform facts.
// GET /api/v1/nodes/{nodeID}/facts/system
func (a *api) handleGetNodeSystemFacts(w http.ResponseWriter, r *http.Request) {
	if a.telemetry == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Telemetri yapılandırılmamış.", false)
		return
	}
	f, err := a.telemetry.GetFacts(r.Context(), chiURLParam(r, "nodeID"))
	if err != nil {
		a.writeTelemetryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleGetNodeMetricsLatest returns the newest metric snapshot.
// GET /api/v1/nodes/{nodeID}/metrics/latest
func (a *api) handleGetNodeMetricsLatest(w http.ResponseWriter, r *http.Request) {
	if a.telemetry == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Telemetri yapılandırılmamış.", false)
		return
	}
	snap, err := a.telemetry.LatestMetrics(r.Context(), chiURLParam(r, "nodeID"))
	if err != nil {
		a.writeTelemetryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// handleListNodeMetrics returns metric history, newest first.
// GET /api/v1/nodes/{nodeID}/metrics?limit=
func (a *api) handleListNodeMetrics(w http.ResponseWriter, r *http.Request) {
	if a.telemetry == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Telemetri yapılandırılmamış.", false)
		return
	}
	list, err := a.telemetry.ListMetrics(r.Context(), chiURLParam(r, "nodeID"), parseIntDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		a.writeTelemetryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

// writeTelemetryError maps telemetry domain errors to API responses.
func (a *api) writeTelemetryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, telemetry.ErrNotFound):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Telemetri kaydı bulunamadı.", false)
	case errors.Is(err, telemetry.ErrInvalidSnapshot):
		a.writeError(w, r, http.StatusBadRequest, "invalid_snapshot", "Geçersiz telemetri verisi.", false)
	default:
		a.logger.Error("telemetry_endpoint_failed",
			slog.String("error", err.Error()),
			slog.String("request_id", requestIDFrom(r.Context())))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Telemetri sorgusu başarısız.", false)
	}
}
