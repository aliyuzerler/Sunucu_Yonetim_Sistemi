//go:build unix

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
)

const (
	defaultEndpoint   = "/run/server-control/privd.sock"
	defaultAuditPathV = "/var/log/server-control/privd-audit.jsonl"
	defaultDockerSock = dockerx.DefaultSocket
)

func defaultAuditPath() string { return defaultAuditPathV }

// registerPlatformFlags adds the unix flags.
func registerPlatformFlags() {
	flag.StringVar(&platformFlags.endpoint, "socket", defaultEndpoint, "dinlenecek Unix socket yolu")
	flag.StringVar(&platformFlags.identity, "allow-uid", "", "izinli UID listesi, virgüllerle (zorunlu)")
	flag.StringVar(&platformFlags.group, "group", "server-agent", "socket grubu (agent kullanıcısının grubu)")
	flag.StringVar(&platformFlags.dockerSocket, "docker-socket", defaultDockerSock, "Docker Engine unix soketi (boş = docker ailesi kapalı)")
	flag.BoolVar(&platformFlags.webEnable, "web-enable", false, "Faz 08 web hosting ailesini etkinleştir (nginx/PHP-FPM site yaşam döngüsü; VARSAYILAN KAPALI)")
}

func run(logger *slog.Logger, cfg daemonConfig) error {
	uids, err := parseUIDs(platformFlags.identity)
	if err != nil || len(uids) == 0 {
		return fmt.Errorf("--allow-uid zorunlu: en az bir UID verin (ör. agent kullanıcısı)")
	}
	sink, err := auditSinkOrDie(logger, cfg.auditFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.auditFile), 0o750); err != nil {
		return fmt.Errorf("audit dizini oluşturulamadı: %w", err)
	}

	// Socket dizini: root'a ait 0750; agent yalnızca grup üzerinden yazar.
	dir := filepath.Dir(platformFlags.endpoint)
	if err := os.MkdirAll(dir, broker.SocketDirPerms); err != nil {
		return fmt.Errorf("socket dizini oluşturulamadı: %w", err)
	}
	_ = os.Remove(platformFlags.endpoint) // kalıntı socket temizliği

	ln, err := net.Listen("unix", platformFlags.endpoint)
	if err != nil {
		return fmt.Errorf("socket dinlenemedi %s: %w", platformFlags.endpoint, err)
	}
	if err := os.Chmod(platformFlags.endpoint, broker.SocketFilePerms); err != nil {
		return fmt.Errorf("socket izni ayarlanamadı: %w", err)
	}
	if err := chownToGroup(platformFlags.endpoint, platformFlags.group); err != nil {
		return fmt.Errorf("socket grubu ayarlanamadı (%s): %w", platformFlags.group, err)
	}

	srv := broker.NewServer(broker.Config{
		AllowedUIDs:   uids,
		MaxConcurrent: cfg.maxConc,
		ExecTimeout:   timeoutDuration(cfg.timeout),
		Systemd:       &svcops.Manager{},
		Pkg:           &pkgops.Runner{},                               // Faz 06: salt-okuma paket envanteri
		Firewall:      &fwops.Runner{},                                // Faz 06: firewall tespit + salt-okuma liste
		Docker:        dockerRunnerOrZero(platformFlags.dockerSocket), // Faz 07: Engine API (unix soket; ADR-0011)
		Web:           webRunnerOrZero(platformFlags.webEnable),       // Faz 08: site yaşam döngüsü (opt-in; ADR-0012)
		Audit:         sink,
		Logger:        logger,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("server-privd hazır",
		slog.String("socket", platformFlags.endpoint),
		slog.Any("allow_uids", uids),
		slog.String("audit", cfg.auditFile))
	err = srv.Serve(ctx, ln)
	logger.Info("server-privd durdu")
	_ = os.Remove(platformFlags.endpoint)
	return err
}

// dockerRunnerOrZero wires the Faz 07 Engine API runner; empty flag disables
// the docker family (ops rejected unsupported, capabilities unavailable).
func dockerRunnerOrZero(socket string) *dockerx.Client {
	if socket == "" {
		return nil
	}
	return &dockerx.Client{Socket: socket}
}

// webRunnerOrZero wires the Faz 08 web hosting runner; the family is OPT-IN
// (default OFF) because it performs real system mutations — useradd, atomic
// config writes, service reloads (ADR-0012).
func webRunnerOrZero(enabled bool) broker.WebRunner {
	if !enabled {
		return nil
	}
	return webops.NewRunner()
}

func parseUIDs(s string) ([]uint32, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("boş")
	}
	var out []uint32
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		u, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("UID çözülemedi %q: %w", part, err)
		}
		out = append(out, uint32(u))
	}
	return out, nil
}

// chownToGroup sets the socket group so the unprivileged agent may connect.
func chownToGroup(path, groupName string) error {
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("grup bulunamadı: %w", err)
	}
	gid, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		return err
	}
	return os.Chown(path, -1, int(gid))
}
