// Package httpapi implements the browser-facing REST API of the control plane.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/identity"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
	"github.com/go-chi/chi/v5"
)

// ErrNotConfigured is returned by the no-op pinger when no database URL is set.
var ErrNotConfigured = errors.New("database not configured")

// AuditLister reads audit entries (implemented by audit.Service).
type AuditLister interface {
	List(ctx context.Context, f audit.Filter) (audit.Page, error)
}

// EnrollmentManager issues/lists/revokes agent enrollment tokens
// (implemented by enroll.Service).
type EnrollmentManager interface {
	IssueToken(ctx context.Context, actor string, in enroll.IssueInput) (identity.Token, *enroll.Token, error)
	List(ctx context.Context) ([]enroll.Token, error)
	Revoke(ctx context.Context, id string) error
}

// Options wires the API server to its dependencies.nil services keep the
// skeleton usable (endpoints degrade with 503 not_configured).
type Options struct {
	Cfg    config.Config
	Logger *slog.Logger
	Pinger Pinger

	Identity  *identity.Service
	Authz     *rbac.Service
	Inventory *inventory.Service
	Tasks     *tasks.Service
	Telemetry *telemetry.Service
	Audit     audit.Recorder
	AuditList AuditLister
	Enroll    EnrollmentManager
}

// api holds handler dependencies.
type api struct {
	cfg       config.Config
	logger    *slog.Logger
	pinger    Pinger
	identity  *identity.Service
	authz     *rbac.Service
	inventory *inventory.Service
	enroll    EnrollmentManager
	tasks     *tasks.Service
	telemetry *telemetry.Service
	audit     audit.Recorder
	auditList AuditLister
}

// New assembles the HTTP handler with the full middleware chain.
// Order (outermost first): requestID → securityHeaders → limitBody →
// recoverer → logging → timeout → authenticate → routes.
func New(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	pinger := opts.Pinger
	if pinger == nil {
		pinger = pingerFunc(func(context.Context) error { return ErrNotConfigured })
	}

	a := &api{
		cfg:       opts.Cfg,
		logger:    logger,
		pinger:    pinger,
		identity:  opts.Identity,
		authz:     opts.Authz,
		inventory: opts.Inventory,
		tasks:     opts.Tasks,
		telemetry: opts.Telemetry,
		audit:     opts.Audit,
		auditList: opts.AuditList,
		enroll:    opts.Enroll,
	}

	r := chi.NewRouter()

	r.Use(requestID)
	r.Use(securityHeaders)
	r.Use(limitBody(opts.Cfg.RequestBodyLimit))
	r.Use(recoverer(logger))
	r.Use(logging(logger))
	r.Use(timeout(opts.Cfg.WriteTimeout))
	r.Use(a.authenticate)

	r.NotFound(a.notFound)
	r.MethodNotAllowed(a.methodNotAllowed)

	// Process health: never behind auth (load balancer probes).
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(pinger, func(req *http.Request, err error) {
		logger.Error("readyz_database_down",
			slog.String("request_id", requestIDFrom(req.Context())),
			slog.String("error", err.Error()),
		)
	}))
	r.Get("/api/v1/version", version)

	// Kimlik: bootstrap ve login oturumsuz; kalan uçlar oturum ister.
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/bootstrap/admin", a.handleBootstrapAdmin)
		r.Post("/auth/login", a.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(a.requireAuth)
			r.Get("/auth/session", a.handleSession)
			r.Get("/auth/csrf", a.handleCSRF)
			r.With(a.requireCSRF).Post("/auth/logout", a.handleLogout)

			// Node envanteri.
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes", a.handleListNodes)
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes/{nodeID}", a.handleGetNode)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Post("/nodes", a.handleCreateNode)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Patch("/nodes/{nodeID}", a.handleUpdateNode)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Put("/nodes/{nodeID}/tags", a.handleSetTags)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Put("/nodes/{nodeID}/facts", a.handleSetFacts)
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes/{nodeID}/capabilities", a.handleListCapabilities)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Post("/nodes/{nodeID}/capabilities", a.handleUpsertCapability)

			// Node grupları.
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/node-groups", a.handleListGroups)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Post("/node-groups", a.handleCreateGroup)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Put("/nodes/{nodeID}/groups", a.handleAssignGroups)

			// Agent kayıt token'ları (Faz 03).
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/agent-enrollments", a.handleListEnrollments)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Post("/agent-enrollments", a.handleIssueEnrollment)
			r.With(a.requirePermission(rbac.PermNodesWrite)).Post("/agent-enrollments/{tokenID}/revoke", a.handleRevokeEnrollment)

			// Node telemetri (Faz 04).
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes/{nodeID}/facts/system", a.handleGetNodeSystemFacts)
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes/{nodeID}/metrics/latest", a.handleGetNodeMetricsLatest)
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/nodes/{nodeID}/metrics", a.handleListNodeMetrics)

			// Görevler.
			r.With(a.requirePermission(rbac.PermTasksRead)).Get("/tasks", a.handleListTasks)
			r.With(a.requirePermission(rbac.PermTasksRead)).Get("/tasks/{taskID}", a.handleGetTask)
			r.With(a.requirePermission(rbac.PermTasksWrite)).Post("/tasks", a.handleCreateTask)
			r.With(a.requirePermission(rbac.PermTasksWrite)).Post("/tasks/{taskID}/cancel", a.handleCancelTask)
			r.With(a.requirePermission(rbac.PermTasksRead)).Get("/task-operations", a.handleListOperations)

			// Audit ve kullanıcılar (salt okunur; yönetim Faz 04 UI).
			r.With(a.requirePermission(rbac.PermAuditRead)).Get("/audit", a.handleListAudit)
			r.With(a.requirePermission(rbac.PermUsersRead)).Get("/users", a.handleListUsers)
			r.With(a.requirePermission(rbac.PermNodesRead)).Get("/overview", a.handleOverview)
		})
	})

	return r
}

func (a *api) notFound(w http.ResponseWriter, req *http.Request) {
	a.writeError(w, req, http.StatusNotFound, "not_found", "İstenen kaynak bulunamadı.", false)
}

func (a *api) methodNotAllowed(w http.ResponseWriter, req *http.Request) {
	a.writeError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "Yöntem desteklenmiyor.", false)
}
