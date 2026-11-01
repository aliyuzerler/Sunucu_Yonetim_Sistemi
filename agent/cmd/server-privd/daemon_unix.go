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
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

const (
	defaultEndpoint   = "/run/server-control/privd.sock"
	defaultAuditPathV = "/var/log/server-control/privd-audit.jsonl"
)

func defaultAuditPath() string { return defaultAuditPathV }

// registerPlatformFlags adds the unix flags.
func registerPlatformFlags() {
	flag.StringVar(&platformFlags.endpoint, "socket", defaultEndpoint, "dinlenecek Unix socket yolu")
	flag.StringVar(&platformFlags.identity, "allow-uid", "", "izinli UID listesi, virgüllerle (zorunlu)")
	flag.StringVar(&platformFlags.group, "group", "server-agent", "socket grubu (agent kullanıcısının grubu)")
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
