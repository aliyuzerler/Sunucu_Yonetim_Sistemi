package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/go-chi/chi/v5"
)

// ErrNotConfigured is returned by the no-op pinger when no database URL is set.
var ErrNotConfigured = errors.New("database not configured")

// Options wires the API server to its dependencies.
type Options struct {
	Cfg    config.Config
	Logger *slog.Logger
	Pinger Pinger
}

// New assembles the HTTP handler with the full middleware chain.
// Order (outermost first): requestID → securityHeaders → limitBody →
// recoverer → logging → timeout → routes.
func New(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	pinger := opts.Pinger
	if pinger == nil {
		pinger = pingerFunc(func(context.Context) error { return ErrNotConfigured })
	}

	r := chi.NewRouter()

	r.Use(requestID)
	r.Use(securityHeaders)
	r.Use(limitBody(opts.Cfg.RequestBodyLimit))
	r.Use(recoverer(logger))
	r.Use(logging(logger))
	r.Use(timeout(opts.Cfg.WriteTimeout))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, req, http.StatusNotFound, "not_found", "İstenen kaynak bulunamadı.", false)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, req, http.StatusMethodNotAllowed, "method_not_allowed", "Yöntem desteklenmiyor.", false)
	})

	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(pinger, func(req *http.Request, err error) {
		logger.Error("readyz_database_down",
			slog.String("request_id", requestIDFrom(req.Context())),
			slog.String("error", err.Error()),
		)
	}))
	r.Get("/api/v1/version", version)

	// Future /api/v1 resources mount here (identity, nodes, tasks...).

	return r
}
