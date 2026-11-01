package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
)

// handleListEnrollments lists agent enrollment tokens.
// GET /api/v1/agent-enrollments
func (a *api) handleListEnrollments(w http.ResponseWriter, r *http.Request) {
	if a.enroll == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kayıt hizmeti yapılandırılmamış.", false)
		return
	}
	tokens, err := a.enroll.List(r.Context())
	if err != nil {
		a.logger.Error("enroll_list_failed", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Token listesi alınamadı.", false)
		return
	}
	if tokens == nil {
		tokens = []enroll.Token{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// handleIssueEnrollment creates a single-use enrollment token. The plaintext
// token is returned EXACTLY ONCE in this response and never stored in clear.
// POST /api/v1/agent-enrollments {scope_type, node_id|group_id, ttl_minutes}
func (a *api) handleIssueEnrollment(w http.ResponseWriter, r *http.Request) {
	if a.enroll == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kayıt hizmeti yapılandırılmamış.", false)
		return
	}
	var req struct {
		ScopeType  string `json:"scope_type"`
		NodeID     string `json:"node_id"`
		GroupID    string `json:"group_id"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	in := enroll.IssueInput{
		ScopeType: req.ScopeType,
		NodeID:    req.NodeID,
		GroupID:   req.GroupID,
		TTL:       time.Duration(req.TTLMinutes) * time.Minute,
	}
	tok, view, err := a.enroll.IssueToken(r.Context(), caller(r), in)
	if err != nil {
		a.writeEnrollError(w, r, err)
		return
	}
	_ = a.audit.Record(r.Context(), auditEvent(authFromOrAnonymous(r.Context()),
		"agent.token.issue", "enrollment_token", view.ID, audit.ResultSuccess,
		requestIDFrom(r.Context()),
		map[string]any{"scope_type": view.ScopeType, "ttl_minutes": req.TTLMinutes}))

	// Plain token: YALNIZCA bu yanıtla bir kez döner; DB'de yalnız özeti var.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      view,
		"plaintext":  tok.Plain,
		"expires_at": view.ExpiresAt,
		"warning":    "Bu token yalnızca bir kez gösterilir ve tek kullanımlıktır.",
	})
}

// handleRevokeEnrollment invalidates a pending token.
// POST /api/v1/agent-enrollments/{tokenID}/revoke
func (a *api) handleRevokeEnrollment(w http.ResponseWriter, r *http.Request) {
	if a.enroll == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kayıt hizmeti yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "tokenID")
	if err := a.enroll.Revoke(r.Context(), id); err != nil {
		a.writeEnrollError(w, r, err)
		return
	}
	_ = a.audit.Record(r.Context(), auditEvent(authFromOrAnonymous(r.Context()),
		"agent.token.revoke", "enrollment_token", id, audit.ResultSuccess,
		requestIDFrom(r.Context()), nil))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (a *api) writeEnrollError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, enroll.ErrTokenBad), errors.Is(err, enroll.ErrScopeNotFound),
		errors.Is(err, enroll.ErrTokenRevoked):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Token bulunamadı veya geçersiz.", false)
	case errors.Is(err, enroll.ErrNodeRetired):
		a.writeError(w, r, http.StatusConflict, "node_retired", "Emekli node için token üretilemez.", false)
	case errors.Is(err, enroll.ErrInvalidInput):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	default:
		a.logger.Error("enroll_error", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Beklenmeyen sunucu hatası.", false)
	}
}

// authFromOrAnonymous returns the auth info or nil for anonymous callers.
func authFromOrAnonymous(ctx context.Context) *authInfo {
	info, _ := authFrom(ctx)
	return info
}
