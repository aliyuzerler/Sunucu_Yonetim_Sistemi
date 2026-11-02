package httpapi

import (
	"net/http"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// handleListAudit returns audit entries with allowlisted filters.
// GET /api/v1/audit?actor=&action=&limit=&cursor=&correlation_id=&result=&node_id=&since=&until=
// Faz 13: correlation ID / sonuç / node / zaman aralığı arama kriterleri.
func (a *api) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if a.auditList == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Denetim kaydı yapılandırılmamış.", false)
		return
	}
	q := r.URL.Query()
	since, until := parseTimeParam(q.Get("since")), parseTimeParam(q.Get("until"))
	page, err := a.auditList.List(r.Context(), audit.Filter{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
		Limit:  parseIntDefault(q.Get("limit"), 50),
		Cursor: q.Get("cursor"),

		CorrelationID: q.Get("correlation_id"),
		Result:        q.Get("result"),
		NodeID:        q.Get("node_id"),
		Since:         since,
		Until:         until,
	})
	if err != nil {
		// Geçersiz imleç/filtre vb. istemci hataları.
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// parseTimeParam parses an RFC3339 query value; zero time on absent/invalid
// (invalid values are rejected by the audit layer only when present — here
// we keep the honest behavior: unparseable values are ignored, matching the
// tolerant query-surface pattern used elsewhere).
func parseTimeParam(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// handleOverview returns honest summary counts for the overview cards.
// GET /api/v1/overview
func (a *api) handleOverview(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil || a.tasks == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Servisler yapılandırılmamış.", false)
		return
	}
	nodes, err := a.inventory.CountNodes(r.Context())
	if err != nil {
		a.logger.Error("overview_failed", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Özet alınamadı.", false)
		return
	}
	totalTasks, err := a.tasks.CountTasks(r.Context())
	if err != nil {
		a.logger.Error("overview_failed", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Özet alınamadı.", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"tasks": totalTasks,
	})
}

// handleListUsers lists user accounts (Faz 02 read-only view; management UI
// and user lifecycle arrive with the users module in Faz 04).
// GET /api/v1/users
func (a *api) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if a.identity == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kimlik hizmeti yapılandırılmamış.", false)
		return
	}
	users, err := a.identity.ListUsers(r.Context())
	if err != nil {
		a.logger.Error("users_list_failed", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Kullanıcı listesi alınamadı.", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}
