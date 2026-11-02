// Command control-plane runs the Server Control REST API and its durable
// task worker. Subcommand `bootstrap` prints a single-use first-admin token.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/acmex"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/agentsrv"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certmgmt"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certs"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dbcred"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dns"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/enroll"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/httpapi"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/identity"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/platform"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func main() {
	// Alt komutlar: bootstrap (ilk yönetici), ca init (CA üretimi),
	// enroll-token (agent kayıt tokenı). Kalanı sunucu modudur.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "bootstrap":
			if err := runBootstrap(); err != nil {
				fmt.Fprintf(os.Stderr, "control-plane bootstrap: %v\n", err)
				os.Exit(1)
			}
			return
		case "ca":
			if err := runCA(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "control-plane ca: %v\n", err)
				os.Exit(1)
			}
			return
		case "enroll-token":
			if err := runEnrollToken(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "control-plane enroll-token: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "control-plane: %v\n", err)
		os.Exit(1)
	}
}

// runCA implements `ca init --cert <path> --key <path> [--cn <name>]`.
func runCA(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("kullanım: control-plane ca init --cert <yol> --key <yol>")
	}
	certPath, keyPath, cn := "", "", ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--cert":
			i++
			if i < len(args) {
				certPath = args[i]
			}
		case "--key":
			i++
			if i < len(args) {
				keyPath = args[i]
			}
		case "--cn":
			i++
			if i < len(args) {
				cn = args[i]
			}
		}
	}
	if certPath == "" || keyPath == "" {
		return errors.New("--cert ve --key zorunlu")
	}
	if err := certs.GenerateCA(certPath, keyPath, cn); err != nil {
		return err
	}
	fmt.Println("CA üretildi:")
	fmt.Println("  sertifika:", certPath)
	fmt.Println("  anahtar  :", keyPath, "(0600)")
	fmt.Println("Agent'lara güven için ca.crt dosyasını kopyalayın (gizli değildir).")
	return nil
}

// runEnrollToken implements `enroll-token --scope node:<id>|group:<id> [--ttl 15m]`.
func runEnrollToken(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("yapılandırma: %w", err)
	}
	if !cfg.DatabaseURLSet {
		return errors.New("CP_DATABASE_URL tanımlı olmalı")
	}
	scope, ttl := "", 15*time.Minute
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			i++
			if i < len(args) {
				scope = args[i]
			}
		case "--ttl":
			i++
			if i < len(args) {
				if d, err := time.ParseDuration(args[i]); err == nil {
					ttl = d
				}
			}
		}
	}
	if scope == "" {
		return errors.New("--scope node:<uuid> veya group:<uuid> zorunlu")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL, db.DefaultPoolConfig(), nil)
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := enroll.NewService(pool, nil)
	in := enroll.IssueInput{ScopeType: enroll.ScopeNode, TTL: ttl}
	if strings.HasPrefix(scope, "node:") {
		in.NodeID = strings.TrimPrefix(scope, "node:")
	} else if strings.HasPrefix(scope, "group:") {
		in.ScopeType = enroll.ScopeGroup
		in.GroupID = strings.TrimPrefix(scope, "group:")
	} else {
		return errors.New("--scope node:<uuid> veya group:<uuid> biçimi gerekli")
	}
	tok, t, err := svc.IssueToken(ctx, "cli", in)
	if err != nil {
		return err
	}
	fmt.Println("Tek kullanımlık agent kayıt tokenı:")
	fmt.Println()
	fmt.Println("  " + tok.Plain)
	fmt.Println()
	fmt.Printf("Kapsam: %s (%s) · Geçerlilik: %s\n", t.ScopeType, scope, t.ExpiresAt.Format(time.RFC3339))
	fmt.Println("Agent tarafında: server-agent enroll --server <host:9090> --ca-file ca.crt --token <TOKEN>")
	return nil
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

		// Faz 09: DNS sağlayıcı + sertifika yaşam döngüsü servisleri.
		var secretsKey secrets.Key
		if cfg.SecretsKey != "" {
			key, keyErr := secrets.ParseKey(cfg.SecretsKey)
			if keyErr != nil {
				return fmt.Errorf("secrets anahtarı: %w", keyErr)
			}
			secretsKey = key
		}
		dnsSvc := &dns.Service{Store: dns.NewStore(p), Key: secretsKey}
		certStore := certmgmt.NewStore(p)
		var acmeMgr *acmex.Manager
		if cfg.ACMEDirectoryURL != "" && cfg.ACMEAccountKey != "" {
			acctKey, keyErr := acmex.LoadOrCreateAccountKey(cfg.ACMEAccountKey)
			if keyErr != nil {
				return fmt.Errorf("ACME hesap anahtarı: %w", keyErr)
			}
			acmeMgr = &acmex.Manager{
				Key:          acctKey,
				DirectoryURL: cfg.ACMEDirectoryURL,
				MaxRetry:     2,
			}
			if cfg.ACMEContact != "" {
				acmeMgr.Contact = []string{cfg.ACMEContact}
			}
		}
		certSvc := &certmgmt.Service{
			Store:   certStore,
			DNS:     dnsSvc,
			ACME:    acmeMgr,
			Secrets: secretsKey,
		}

		taskRegistry, err := tasks.BuildDefaultRegistry(inventorySvc, tasks.Faz09Services{DNS: dnsSvc, Cert: certSvc})
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
		// Faz 09: sertifika süre izleyicisi (issued → expired geçişi).
		go certmgmt.Watcher{Store: certStore, Interval: time.Hour}.Run(workerCtx)

		var ca *certs.CA
		if cfg.CACert != "" && cfg.CAKey != "" {
			ca, err = certs.LoadCA(cfg.CACert, cfg.CAKey)
			if err != nil {
				return fmt.Errorf("agent CA: %w", err)
			}
		}
		enrollSvc := enroll.NewService(p, ca)

		telemetrySvc := telemetry.NewService(telemetry.NewPGStore(p))

		opts.Identity = identitySvc
		opts.Authz = authzSvc
		opts.Inventory = inventorySvc
		opts.Tasks = tasksSvc
		opts.Telemetry = telemetrySvc
		opts.Audit = auditRec
		opts.AuditList = audit.NewService(p)
		opts.Enroll = enrollSvc
		opts.DNS = dnsSvc
		opts.Certs = certStore
		// Faz 10: DB kullanıcı şifreleri — şifre seal edilip tek-seferlik
		// reveal yüzeyine ayrıştırılır (CP_SECRETS_KEY yoksa da kurulur:
		// redaksiyon daima kazanır, sadece reveal imkânsızlaşır).
		dbCredSvc := &dbcred.Service{Store: dbcred.NewStore(p), Key: secretsKey, KeyConfigured: cfg.SecretsKey != ""}
		opts.DBCreds = dbCredSvc

		// Agent gRPC sunucusu: yalnızca CA yapılandırıldığında başlar.
		if ca != nil {
			serverCert, err := ca.IssueServerCert(
				[]string{"localhost", hostnameOrLocal()},
				[]string{"127.0.0.1", "::1"},
				0)
			if err != nil {
				return fmt.Errorf("agent sunucu sertifikası: %w", err)
			}
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{serverCert},
				ClientAuth:   tls.VerifyClientCertIfGiven,
				ClientCAs:    certPoolWith(ca.Cert),
				MinVersion:   tls.VersionTLS12,
			}
			agentServer := agentsrv.New(agentsrv.Services{
				CA:             ca,
				Enroll:         enrollSvc,
				Inventory:      inventorySvc,
				Tasks:          tasksSvc,
				TaskStore:      taskStore,
				Telemetry:      telemetrySvc,
				Audit:          auditRec,
				Logger:         logger,
				ResultScrubber: dbCredSvc, // Faz 10: persist öncesi şifre seal+ayrıştırma
			}, logger)
			grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
			agentServer.RegisterGRPC(grpcSrv)

			grpcListener, err := net.Listen("tcp", cfg.GRPCAddr)
			if err != nil {
				return fmt.Errorf("agent gRPC dinleyicisi: %w", err)
			}
			go func() {
				logger.Info("agent_grpc_listening", slog.String("addr", cfg.GRPCAddr))
				if err := grpcSrv.Serve(grpcListener); err != nil {
					logger.Error("agent_grpc_serve_failed", slog.String("error", err.Error()))
				}
			}()
			defer grpcSrv.Stop()
		} else {
			logger.Warn("agent_grpc_disabled", slog.String("hint", "CP_CA_CERT/CP_CA_KEY tanımlayın (control-plane ca init)"))
		}
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

// certPoolWith builds a CA pool for client certificate verification.
func certPoolWith(ca *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pool
}

// hostnameOrLocal returns the OS hostname (fallback: "localhost").
func hostnameOrLocal() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "localhost"
	}
	return h
}
