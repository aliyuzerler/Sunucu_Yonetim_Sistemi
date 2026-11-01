// Command control-plane starts the Server Control REST API (Faz 01 skeleton).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/httpapi"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("yapılandırma: %w", err)
	}
	logger := newLogger(cfg)
	slog.SetDefault(logger)

	logger.Info("control_plane_starting",
		slog.String("version", platform.Version),
		slog.String("addr", cfg.HTTPAddr),
		slog.String("log_level", cfg.LogLevel),
	)
	if cfg.DatabaseURLSet {
		logger.Info("database_configured",
			slog.String("target", config.RedactDatabaseURL(cfg.DatabaseURL)))
	} else {
		logger.Warn("database_not_configured", slog.String("hint", "CP_DATABASE_URL ortam değişkeni ayarlayın; /readyz degraded döner"))
	}

	// Veritabanı havuzu: lazy bağlanır; DB kapalıyken de healthz çalışır.
	var pool poolLike
	pinger := httpapi.Pinger(httpapiPingerNotConfigured{})
	if cfg.DatabaseURLSet {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		p, err := db.NewPool(ctx, cfg.DatabaseURL, db.DefaultPoolConfig(), logger)
		cancel()
		if err != nil {
			return fmt.Errorf("veritabanı havuzu: %w", err)
		}
		pool = p
		pinger = p
		defer pool.Close()
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(httpapi.Options{Cfg: cfg, Logger: logger, Pinger: pinger}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http sunucusu: %w", err)
	case <-ctx.Done():
	}

	logger.Info("graceful_shutdown_started", slog.Duration("grace", cfg.ShutdownGrace))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("control_plane_stopped")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// poolLike is the minimal interface main needs from the pool.
type poolLike interface {
	Close()
}

// httpapiPingerNotConfigured reports the not-configured state to /readyz.
type httpapiPingerNotConfigured struct{}

func (httpapiPingerNotConfigured) Ping(context.Context) error {
	return httpapi.ErrNotConfigured
}
