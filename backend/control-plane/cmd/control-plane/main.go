// Command control-plane runs the Server Control REST API and its durable
// task worker. Subcommand `bootstrap` prints a single-use first-admin token.
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

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/httpapi"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/identity"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/platform"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
)

func main() {
	// bootstrap alt komutu: ilk yönetici kurulum tokenı üretir (tek kullanımlık).
	if len(os.Args) > 1 && os.Args[1] == "bootstrap" {
		if err := runBootstrap(); err != nil {
			fmt.Fprintf(os.Stderr, "control-plane bootstrap: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %v\n", err)
		os.Exit(1)
	}
}

// runBootstrap issues a one-time admin bootstrap token. It must run on the
// control plane host by an operator; there is no HTTP endpoint that issues
// bootstrap tokens (first-run privilege escalation guard).
func runBootstrap() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("yapılandırma: %w", err)
	}
	if !cfg.DatabaseURLSet {
		return errors.New("CP_DATABASE_URL tanımlı olmalı (bootstrap veritabanına yazar)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.DefaultPoolConfig(), nil)
	if err != nil {
		return err
	}
	defer pool.Close()

	store := identity.NewPGStore(pool)
	svc := identity.NewService(store, identity.Config{BootstrapTTL: cfg.BootstrapTTL})
	tok, err := svc.IssueBootstrapToken(ctx)
	if err != nil {
		return err
	}

	fmt.Println("Tek kullanımlık yönetici kurulum tokenı:")
	fmt.Println()
	fmt.Println("  " + tok.Plain)
	fmt.Println()
	fmt.Printf("Geçerlilik süresi: %s. Kullanım (POST, token gizli tutun):\n", cfg.BootstrapTTL)
	fmt.Println()
	fmt.Println("  curl -X POST http://<control-plane>:8080/api/v1/bootstrap/admin \\")
	fmt.Println("    -H 'Content-Type: application/json' \\")
	fmt.Println(`    -d '{"token":"<TOKEN>","username":"<yönetici-adı>","password":"<güçlü-parola>"}'`)
	fmt.Println()
	fmt.Println("Token tek kullanımlıktır ve veritabanında yalnızca SHA-256 özeti saklanır.")
	return nil
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

	var opts httpapi.Options
	opts.Cfg = cfg
	opts.Logger = logger
	opts.Pinger = pinger

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

		auditRec := audit.NewPGRecorder(p)
		identitySvc := identity.NewService(identity.NewPGStore(p), identity.Config{
			SessionTTL:   cfg.SessionTTL,
			BootstrapTTL: cfg.BootstrapTTL,
		})
		authzSvc := rbac.NewService(rbac.NewPGStore(p))
		inventorySvc := inventory.NewService(inventory.NewPGStore(p), auditRec)
		taskStore := tasks.NewPGStore(p)
		taskRegistry, err := tasks.BuildDefaultRegistry(inventorySvc)
		if err != nil {
			return fmt.Errorf("görev kayıt defteri: %w", err)
		}
		tasksSvc := tasks.NewService(taskStore, taskRegistry, auditRec)
		taskWorker := tasks.NewWorker(taskStore, taskRegistry, nil, auditRec, logger, tasks.WorkerConfig{
			OwnerID: fmt.Sprintf("control-plane-%d", os.Getpid()),
		})

		workerCtx, workerStop := context.WithCancel(context.Background())
		defer workerStop()
		go taskWorker.Run(workerCtx)

		opts.Identity = identitySvc
		opts.Authz = authzSvc
		opts.Inventory = inventorySvc
		opts.Tasks = tasksSvc
		opts.Audit = auditRec
		opts.AuditList = audit.NewService(p)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(opts),
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
