// Command server-privd is the local privileged broker (Faz 04). It runs as
// root, listens ONLY on a mode-0660 Unix socket, verifies peer identity with
// SO_PEERCRED against an explicit UID allowlist, executes ONLY the typed
// allowlisted service operations via systemctl argument vectors (never shell
// strings) and writes one JSONL audit line per handled request.
//
// It has NO network listeners: the agent dials outbound to the control plane
// and forwards validated requests to the broker over the local socket.
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
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/agent"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

func main() {
	var (
		socketPath = flag.String("socket", "/run/server-control/privd.sock", "dinlenecek Unix socket yolu")
		group      = flag.String("group", "server-agent", "socket grubu (agent kullanıcısının grubu)")
		allowUIDs  = flag.String("allow-uid", "", "izinli UID listesi, virgülle ayrılmış (zorunlu)")
		auditFile  = flag.String("audit-file", "/var/log/server-control/privd-audit.jsonl", "audit JSONL dosyası")
		maxConc    = flag.Int("max-concurrent", 4, "aynı anda çalışacak işlem sayısı")
		timeoutS   = flag.Int("timeout-seconds", 60, "işlem başına zaman aşımı (saniye)")
		showVer    = flag.Bool("version", false, "sürümü yaz ve çık")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("server-privd", agent.Version)
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger, runConfig{
		socketPath: *socketPath, group: *group, allowUIDs: *allowUIDs,
		auditFile: *auditFile, maxConc: *maxConc, timeout: *timeoutS,
	}); err != nil {
		logger.Error("server-privd çıktı", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

type runConfig struct {
	socketPath string
	group      string
	allowUIDs  string
	auditFile  string
	maxConc    int
	timeout    int
}

func run(logger *slog.Logger, cfg runConfig) error {
	uids, err := parseUIDs(cfg.allowUIDs)
	if err != nil || len(uids) == 0 {
		return fmt.Errorf("--allow-uid zorunlu: en az bir UID verin (ör. agent kullanıcısı)")
	}
	sink, err := broker.NewJSONLSink(cfg.auditFile)
	if err != nil {
		return fmt.Errorf("audit dosyası açılamadı %s: %w", cfg.auditFile, err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.auditFile), 0o750); err != nil {
		return fmt.Errorf("audit dizini oluşturulamadı: %w", err)
	}

	// Socket dizini: root'a ait 0750; agent yalnızca grup üzerinden yazar.
	dir := filepath.Dir(cfg.socketPath)
	if err := os.MkdirAll(dir, broker.SocketDirPerms); err != nil {
		return fmt.Errorf("socket dizini oluşturulamadı: %w", err)
	}
	_ = os.Remove(cfg.socketPath) // kalıntı socket temizliği (bind sonrası yenisi kurulur)

	ln, err := net.Listen("unix", cfg.socketPath)
	if err != nil {
		return fmt.Errorf("socket dinlenemedi %s: %w", cfg.socketPath, err)
	}
	if err := os.Chmod(cfg.socketPath, broker.SocketFilePerms); err != nil {
		return fmt.Errorf("socket izni ayarlanamadı: %w", err)
	}
	if err := chownToGroup(cfg.socketPath, cfg.group); err != nil {
		return fmt.Errorf("socket grubu ayarlanamadı (%s): %w", cfg.group, err)
	}

	srv := broker.NewServer(broker.Config{
		AllowedUIDs:   uids,
		MaxConcurrent: cfg.maxConc,
		ExecTimeout:   time.Duration(cfg.timeout) * time.Second,
		Systemd:       &svcops.Manager{},
		Audit:         sink,
		Logger:        logger,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("server-privd hazır",
		slog.String("socket", cfg.socketPath),
		slog.Any("allow_uids", uids),
		slog.String("audit", cfg.auditFile))
	err = srv.Serve(ctx, ln)
	logger.Info("server-privd durdu")
	_ = os.Remove(cfg.socketPath)
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
