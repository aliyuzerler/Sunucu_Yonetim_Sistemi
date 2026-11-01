package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/identity"
)

// clientIP extracts the direct peer IP (no proxy chain trust in Faz 02).
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// setSessionCookie writes the session cookie with secure attributes.
func (a *api) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// handleLogin authenticates a user and issues the session cookie + CSRF token.
// POST /api/v1/auth/login {username, password}
func (a *api) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.identity == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kimlik hizmeti yapılandırılmamış.", false)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Username == "" || req.Password == "" {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "Kullanıcı adı ve parola gerekli.", false)
		return
	}

	res, err := a.identity.Login(r.Context(), identity.LoginInput{
		Username: req.Username,
		Password: req.Password,
		SourceIP: clientIP(r),
	})
	switch {
	case err == nil:
	case errors.Is(err, identity.ErrTooManyAttempts):
		a.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Çok fazla deneme; birkaç dakika sonra tekrar deneyin.", true)
		return
	case errors.Is(err, identity.ErrUserDisabled):
		_ = a.audit.Record(r.Context(), auditEvent(nil, "auth.login", "session", "", audit.ResultDenied, requestIDFrom(r.Context()), map[string]any{"username": req.Username, "reason": "disabled"}))
		a.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Kullanıcı adı veya parola hatalı.", false)
		return
	default:
		_ = a.audit.Record(r.Context(), auditEvent(nil, "auth.login", "session", "", audit.ResultFailure, requestIDFrom(r.Context()), map[string]any{"username": req.Username}))
		a.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Kullanıcı adı veya parola hatalı.", false)
		return
	}

	a.setSessionCookie(w, res.Token, int(a.identity.SessionTTL().Seconds()))
	_ = a.audit.Record(r.Context(), auditEvent(&authInfo{UserID: res.Session.UserID, Username: res.Session.Username},
		"auth.login", "session", res.Session.ID, audit.ResultSuccess, requestIDFrom(r.Context()), nil))

	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":       res.Session.UserID,
			"username": res.Session.Username,
		},
		"csrf_token": res.CSRFToken,
		"expires_at": res.Session.ExpiresAt,
	})
}

// handleLogout revokes the current session.
// POST /api/v1/auth/logout
func (a *api) handleLogout(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r.Context())
	if a.identity != nil && info != nil {
		if err := a.identity.Logout(r.Context(), info.SessionID); err != nil {
			a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Oturum kapatılamadı.", false)
			return
		}
		_ = a.audit.Record(r.Context(), auditEvent(info, "auth.logout", "session", info.SessionID, audit.ResultSuccess, requestIDFrom(r.Context()), nil))
	}
	a.setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleSession returns the current session and user identity.
// GET /api/v1/auth/session
func (a *api) handleSession(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r.Context())
	if !ok {
		a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Oturum gerekli.", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":     info.UserID,
		"username":    info.Username,
		"session_id":  info.SessionID,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleCSRF returns the CSRF token derived from the presented session.
// GET /api/v1/auth/csrf
func (a *api) handleCSRF(w http.ResponseWriter, r *http.Request) {
	info, ok := authFrom(r.Context())
	if !ok {
		a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Oturum gerekli.", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"csrf_token": identity.DeriveCSRF(info.Token),
	})
}

// handleBootstrapAdmin consumes the one-time bootstrap token and creates the
// first admin. Token issuance happens ONLY on the host via the CLI
// (`control-plane bootstrap`), never over HTTP.
// POST /api/v1/bootstrap/admin {token, username, password, display_name}
func (a *api) handleBootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	if a.identity == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Kimlik hizmeti yapılandırılmamış.", false)
		return
	}
	var req struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Token == "" || req.Username == "" || req.Password == "" {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "token, username ve password alanları gerekli.", false)
		return
	}

	u, err := a.identity.BootstrapAdmin(r.Context(), identity.BootstrapAdminInput{
		Token:       req.Token,
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
	})
	switch {
	case err == nil:
	case errors.Is(err, identity.ErrBootstrapUnavailable):
		a.writeError(w, r, http.StatusConflict, "bootstrap_done", "Kurulum zaten tamamlanmış.", false)
		return
	case errors.Is(err, identity.ErrBootstrapTokenBad),
		errors.Is(err, identity.ErrBootstrapTokenConsumed),
		errors.Is(err, identity.ErrBootstrapTokenExpired),
		errors.Is(err, identity.ErrInvalidUsername):
		_ = a.audit.Record(r.Context(), auditEvent(nil, "auth.bootstrap", "user", "", audit.ResultDenied, requestIDFrom(r.Context()), map[string]any{"reason": err.Error()}))
		a.writeError(w, r, http.StatusBadRequest, "bootstrap_rejected", "Bootstrap reddedildi: token/kimlik bilgileri geçersiz.", false)
		return
	default:
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Bootstrap tamamlanamadı.", false)
		return
	}

	_ = a.audit.Record(r.Context(), auditEvent(&authInfo{UserID: u.ID, Username: u.Username},
		"auth.bootstrap", "user", u.ID, audit.ResultSuccess, requestIDFrom(r.Context()),
		map[string]any{"username": u.Username}))

	writeJSON(w, http.StatusCreated, map[string]any{
		"user_id":  u.ID,
		"username": u.Username,
		"message":  "İlk yönetici oluşturuldu. Artık giriş yapabilirsiniz.",
	})
}
