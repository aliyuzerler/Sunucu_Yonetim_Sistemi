package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

const sessionCookie = "srvctl_session"

// authInfo carries the authenticated caller through the request context.
type authInfo struct {
	UserID    string
	Username  string
	SessionID string
	Token     string // plaintext session token (cookie value); used to derive CSRF
}

type authCtxKey struct{}

func authFrom(ctx context.Context) (*authInfo, bool) {
	a, ok := ctx.Value(authCtxKey{}).(*authInfo)
	return a, ok && a != nil
}

// authenticate resolves the session cookie (if any) and stores auth info.
// It never rejects: route-level middleware decides what anonymous access gets.
func (a *api) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" && a.identity != nil {
			if sess, serr := a.identity.ResolveSession(r.Context(), c.Value); serr == nil {
				info := &authInfo{
					UserID:    sess.UserID,
					Username:  sess.Username,
					SessionID: sess.ID,
					Token:     c.Value,
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authCtxKey{}, info)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth rejects unauthenticated requests with the stable 401 envelope.
func (a *api) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authFrom(r.Context()); !ok {
			a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Oturum gerekli.", false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireCSRF enforces the derived synchronizer token on state-changing
// methods. SameSite=Strict + custom header: cross-site attackers cannot set
// the header without a CORS-approved preflight.
func (a *api) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		info, ok := authFrom(r.Context())
		if !ok {
			a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Oturum gerekli.", false)
			return
		}
		if !a.identity.CSRFMatches(info.Token, r.Header.Get("X-CSRF-Token")) {
			a.writeError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF doğrulaması başarısız.", false)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkPermission runs one explicit RBAC check inside a handler (used for
// per-operation permissions that a route middleware cannot know, e.g. Faz 04
// service mutations). Denied checks are audited like requirePermission.
func (a *api) checkPermission(r *http.Request, permission, nodeID string) bool {
	info, ok := authFrom(r.Context())
	if !ok {
		return false
	}
	if a.authz == nil {
		return false
	}
	res := rbac.Resource{}
	if nodeID != "" {
		resolved, ok := a.nodeResource(r.Context(), nodeID)
		if !ok {
			return false
		}
		res = resolved
	}
	decision, err := a.authz.Check(r.Context(), info.UserID, permission, res)
	if err != nil {
		a.logger.Error("rbac_check_failed",
			slog.String("error", err.Error()),
			slog.String("request_id", requestIDFrom(r.Context())))
		return false
	}
	if !decision.Allowed {
		_ = a.audit.Record(r.Context(), auditEvent(info, "auth.denied", "permission", permission,
			audit.ResultDenied, requestIDFrom(r.Context()),
			map[string]any{"reason": decision.Reason, "permission": permission}))
		return false
	}
	return true
}

// nodeResource resolves a node-scoped RBAC resource; failures deny access.
func (a *api) nodeResource(ctx context.Context, nodeID string) (rbac.Resource, bool) {
	groups, err := a.inventory.Store().NodeGroupIDs(ctx, nodeID)
	if err != nil {
		return rbac.Resource{}, false
	}
	return rbac.Resource{NodeID: nodeID, NodeGroupIDs: groups}, true
}

// requirePermission builds a middleware checking the permission for the
// current caller. Node-scoped permissions resolve node ID from the chi URL
// parameter "nodeID" when present.
func (a *api) requirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := authFrom(r.Context())
			if !ok {
				a.writeError(w, r, http.StatusUnauthorized, "unauthorized", "Oturum gerekli.", false)
				return
			}
			if a.authz == nil {
				a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Yetkilendirme yapılandırılmamış.", false)
				return
			}
			res := rbac.Resource{}
			if nodeID := chiURLParam(r, "nodeID"); nodeID != "" {
				resolved, ok := a.nodeResource(r.Context(), nodeID)
				if !ok {
					a.writeError(w, r, http.StatusForbidden, "forbidden", "Kaynak erişilemez.", false)
					return
				}
				res = resolved
			}
			decision, err := a.authz.Check(r.Context(), info.UserID, permission, res)
			if err != nil {
				a.logger.Error("rbac_check_failed",
					slog.String("error", err.Error()),
					slog.String("request_id", requestIDFrom(r.Context())))
				a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Yetki denetimi başarısız.", false)
				return
			}
			if !decision.Allowed {
				// Denied access is auditable evidence.
				_ = a.audit.Record(r.Context(), auditEvent(info, "auth.denied", "permission", permission,
					audit.ResultDenied, requestIDFrom(r.Context()),
					map[string]any{"reason": decision.Reason, "permission": permission}))
				a.writeError(w, r, http.StatusForbidden, "forbidden", "Bu işlem için yetkiniz yok.", false)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
