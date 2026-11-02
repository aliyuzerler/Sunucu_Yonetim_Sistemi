package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/alerts"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// AlertRuleCreate is the create-rule request body.
type AlertRuleCreate struct {
	Name            string  `json:"name"`
	NodeID          string  `json:"node_id,omitempty"` // "" = all nodes
	Metric          string  `json:"metric"`
	Dimension       string  `json:"dimension,omitempty"` // disk device
	Op              string  `json:"op"`
	Threshold       float64 `json:"threshold"`
	DurationSeconds int     `json:"duration_seconds,omitempty"`
	Severity        string  `json:"severity"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// handleListAlertRules lists alert rules.
// GET /api/v1/alert-rules
func (a *api) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	includeDisabled := r.URL.Query().Get("include_disabled") == "1"
	rules, err := a.alerts.ListRules(r.Context(), includeDisabled)
	if err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// handleCreateAlertRule creates a rule (audited).
// POST /api/v1/alert-rules
func (a *api) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	var body AlertRuleCreate
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	rule := alerts.Rule{
		Name:            body.Name,
		NodeID:          body.NodeID,
		Metric:          alerts.Metric(body.Metric),
		Dimension:       body.Dimension,
		Op:              alerts.Op(body.Op),
		Threshold:       body.Threshold,
		DurationSeconds: body.DurationSeconds,
		Severity:        alerts.Severity(body.Severity),
		Enabled:         enabled,
	}
	if err := rule.Validate(); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	created, err := a.alerts.CreateRule(r.Context(), rule)
	if err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	a.recordAudit(r, "alert.rule.create", "alert_rule", created.ID, map[string]any{
		"name": created.Name, "metric": string(created.Metric), "node_id": created.NodeID,
	})
	writeJSON(w, http.StatusCreated, created)
}

// handleDeleteAlertRule deletes a rule (audited).
// DELETE /api/v1/alert-rules/{ruleID}
func (a *api) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "ruleID")
	if err := a.alerts.DeleteRule(r.Context(), id); err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	a.recordAudit(r, "alert.rule.delete", "alert_rule", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handlePatchAlertRule toggles rule evaluation.
// PATCH /api/v1/alert-rules/{ruleID} {"enabled": true|false}
func (a *api) handlePatchAlertRule(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Enabled == nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled alanı zorunlu", false)
		return
	}
	id := chiURLParam(r, "ruleID")
	if err := a.alerts.SetRuleEnabled(r.Context(), id, *body.Enabled); err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	a.recordAudit(r, "alert.rule.update", "alert_rule", id, map[string]any{"enabled": *body.Enabled})
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "enabled": *body.Enabled})
}

// handleListAlerts lists alerts (status/node filters).
// GET /api/v1/alerts?status=&node_id=&limit=
func (a *api) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	q := r.URL.Query()
	list, err := a.alerts.ListAlerts(r.Context(), alerts.ListFilter{
		Status: alerts.Status(q.Get("status")),
		NodeID: q.Get("node_id"),
		Limit:  parseIntDefault(q.Get("limit"), 50),
	})
	if err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": list})
}

// handleAcknowledgeAlert marks an alert acknowledged (audited; idempotent
// on open→acknowledged, typed 409 on silenced/resolved).
// POST /api/v1/alerts/{alertID}/acknowledge
func (a *api) handleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	a.alertTransition(w, r, alerts.StatusAcknowledged, nil, "alert.acknowledge")
}

// handleSilenceAlert silences an alert for a duration (audited).
// POST /api/v1/alerts/{alertID}/silence {"duration_minutes": 60}
func (a *api) handleSilenceAlert(w http.ResponseWriter, r *http.Request) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		DurationMinutes int `json:"duration_minutes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	if body.DurationMinutes <= 0 || body.DurationMinutes > 30*24*60 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "duration_minutes 1..43200 olmalı", false)
		return
	}
	until := time.Now().Add(time.Duration(body.DurationMinutes) * time.Minute)
	a.alertTransition(w, r, alerts.StatusSilenced, &until, "alert.silence")
}

// handleResolveAlert resolves an alert (audited).
// POST /api/v1/alerts/{alertID}/resolve
func (a *api) handleResolveAlert(w http.ResponseWriter, r *http.Request) {
	a.alertTransition(w, r, alerts.StatusResolved, nil, "alert.resolve")
}

func (a *api) alertTransition(w http.ResponseWriter, r *http.Request, to alerts.Status, until *time.Time, auditAction string) {
	if a.alerts == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Alarm modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "alertID")
	updated, err := a.alerts.Transition(r.Context(), id, to, caller(r), until)
	if err != nil {
		a.writeAlertError(w, r, err)
		return
	}
	a.recordAudit(r, auditAction, "alert", id, map[string]any{
		"status": string(to), "occurrences": updated.Occurrences,
	})
	writeJSON(w, http.StatusOK, updated)
}

// writeAlertError maps alert domain errors to API responses.
func (a *api) writeAlertError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, alerts.ErrNotFound):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Alarm kaydı bulunamadı.", false)
	case errors.Is(err, alerts.ErrInvalidState):
		a.writeError(w, r, http.StatusConflict, "invalid_state", err.Error(), false)
	case errors.Is(err, alerts.ErrInvalidRule), errors.Is(err, alerts.ErrSilenceExpired):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, alerts.ErrCardinality):
		a.writeError(w, r, http.StatusConflict, "cardinality_bound", err.Error(), false)
	default:
		a.logger.Error("alert_error", slog.String("error", err.Error()), slog.String("request_id", requestIDFrom(r.Context())))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Alarm işlemi başarısız.", false)
	}
}

// recordAudit records one success audit event with the caller identity and
// redacted metadata (shared Faz 13 alert surface).
func (a *api) recordAudit(r *http.Request, action, targetType, targetID string, meta map[string]any) {
	if a.audit == nil {
		return
	}
	info, _ := authFrom(r.Context())
	_ = a.audit.Record(r.Context(), auditEvent(info, action, targetType, targetID, audit.ResultSuccess, requestIDFrom(r.Context()), meta))
}
