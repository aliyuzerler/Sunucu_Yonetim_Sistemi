//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winscm"
	"golang.org/x/sys/windows/svc"
)

const (
	defaultEndpointW   = broker.DefaultPipePath
	defaultAuditPathW  = `C:\ProgramData\server-control\privd-audit.jsonl`
	defaultServiceName = "server-privd"
)

func defaultAuditPath() string { return defaultAuditPathW }

// registerPlatformFlags adds the windows flags.
func registerPlatformFlags() {
	flag.StringVar(&platformFlags.endpoint, "pipe", defaultEndpointW, "dinlenecek named pipe yolu")
	flag.StringVar(&platformFlags.identity, "allow-sid", "", "izinli çağırıcı SID'leri, virgüllerle (zorunlu; örn. agent hesabının S-1-5-21-… SID'i)")
	flag.StringVar(&platformFlags.sddl, "sddl", broker.DefaultSDDL, "pipe güvenlik tanımlayıcısı (SDDL); agent SID'i buraya da eklenmelidir")
}

// run starts the windows broker. LocalSystem servis olarak çalışır
// (deploy/agent-windows/README.md: sc create server-privd …).
func run(logger *slog.Logger, cfg daemonConfig) error {
	sids := parseSIDs(platformFlags.identity)
	if len(sids) == 0 {
		return fmt.Errorf("--allow-sid zorunlu: en az bir SID verin (ör. agent hesabı SID'i)")
	}
	sink, err := auditSinkOrDie(logger, cfg.auditFile)
	if err != nil {
		return err
	}

	// SCM altında mı? Servis olarak: svc.Run üzerinden çalıştır.
	if inService, err := svc.IsWindowsService(); err == nil && inService {
		return svc.Run(defaultServiceName, &privdService{cfg: cfg, sids: sids, sink: sink, logger: logger})
	}

	ln, err := broker.ListenPipe(platformFlags.endpoint, platformFlags.sddl)
	if err != nil {
		return fmt.Errorf("pipe dinlenemedi %s: %w", platformFlags.endpoint, err)
	}
	return serveBroker(logger, cfg, sids, sink, ln)
}

// privdService implements svc.Handler for SCM execution.
type privdService struct {
	cfg    daemonConfig
	sids   []string
	sink   *broker.JSONLSink
	logger *slog.Logger
}

func (s *privdService) Execute(_ []string, req <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ln, err := broker.ListenPipe(platformFlags.endpoint, platformFlags.sddl)
	if err != nil {
		s.logger.Error("pipe dinlenemedi", slog.String("error", err.Error()))
		changes <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
		return false, 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		srv := broker.NewServer(broker.Config{
			AllowedSIDs:   s.sids,
			MaxConcurrent: s.cfg.maxConc,
			ExecTimeout:   timeoutDuration(s.cfg.timeout),
			Scm:           &winscm.Manager{},
			Audit:         s.sink,
			Logger:        s.logger,
		})
		done <- srv.Serve(ctx, ln)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			cancel()
			<-done
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	cancel()
	<-done
	return false, 0
}

// serveBroker is the console (non-SCM) run path; shared by tests/debug.
func serveBroker(logger *slog.Logger, cfg daemonConfig, sids []string, sink *broker.JSONLSink, ln net.Listener) error {
	srv := broker.NewServer(broker.Config{
		AllowedSIDs:   sids,
		MaxConcurrent: cfg.maxConc,
		ExecTimeout:   timeoutDuration(cfg.timeout),
		Scm:           &winscm.Manager{},
		Audit:         sink,
		Logger:        logger,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logger.Info("server-privd hazır",
		slog.String("pipe", platformFlags.endpoint),
		slog.Any("allow_sids", sids),
		slog.String("audit", cfg.auditFile))
	err := srv.Serve(ctx, ln)
	logger.Info("server-privd durdu")
	return err
}

// parseSIDs validates the comma-separated SID allowlist shape (canonical
// "S-1-…" only; no spaces).
func parseSIDs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.HasPrefix(part, "S-1-") || strings.ContainsAny(part, " \t/\\") {
			continue // allowlist shape: skip anything non-canonical
		}
		out = append(out, part)
	}
	return out
}
