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

	"crypto/ed25519"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/backupops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dbops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/mailops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/tlsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/updateops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/vmops"
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
	flag.BoolVar(&platformFlags.dbEnable, "db-enable", false, "Faz 10 veritabanı ailesini etkinleştir (node-local PostgreSQL/MariaDB-MySQL yönetimi; VARSAYILAN KAPALI)")
	flag.BoolVar(&platformFlags.fsEnable, "fs-enable", false, "Faz 10 scoped dosya yöneticisini etkinleştir (yalnız site docroot'ları; VARSAYILAN KAPALI)")
	flag.BoolVar(&platformFlags.mailEnable, "mail-enable", false, "Faz 11 e-posta ailesini etkinleştir (Postfix/Dovecot/Rspamd yönetimi; VARSAYILAN KAPALI)")
	flag.BoolVar(&platformFlags.vmEnable, "vm-enable", false, "Faz 12 sanallaştırma ailesini etkinleştir (KVM/QEMU/libvirt VM yönetimi; VARSAYILAN KAPALI)")
	flag.BoolVar(&platformFlags.backupEnable, "backup-enable", false, "Faz 14 yedekleme ailesini etkinleştir (restic node yedeği; VARSAYILAN KAPALI)")
	flag.StringVar(&platformFlags.backupSources, "backup-source", backupops.DefaultSource, "virgüllü yedek kaynak kökleri (yollar bu köklerin ALTINDA olmalı)")
	flag.BoolVar(&platformFlags.updateEnable, "update-enable", false, "Faz 15 imzalı güncelleme ailesini etkinleştir (staging + health-check + rollback; VARSAYILAN KAPALI — ADR-0020)")
	flag.StringVar(&platformFlags.updateTrust, "update-trust-key", "", "virgüllü base64 ed25519 genel anahtar(lar) — update artifact imza güven kökü (zorunlu)")
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
		Pkg:           &pkgops.Runner{},                                                            // Faz 06: salt-okuma paket envanteri
		Firewall:      &fwops.Runner{},                                                             // Faz 06: firewall tespit + salt-okuma liste
		Docker:        dockerRunnerOrZero(platformFlags.dockerSocket),                              // Faz 07: Engine API (unix soket; ADR-0011)
		Web:           webRunnerOrZero(platformFlags.webEnable),                                    // Faz 08: site yaşam döngüsü (opt-in; ADR-0012)
		TLS:           tlsRunnerOrZero(platformFlags.webEnable),                                    // Faz 09: sertifika yaşam döngüsü (aynı opt-in; ADR-0013)
		DB:            dbRunnerOrZero(platformFlags.dbEnable),                                      // Faz 10: DB yönetimi (opt-in; ADR-0014)
		Fs:            fsRunnerOrZero(platformFlags.fsEnable),                                      // Faz 10: dosya yöneticisi (opt-in; ADR-0014)
		Mail:          mailRunnerOrZero(platformFlags.mailEnable),                                  // Faz 11: e-posta yönetimi (opt-in; ADR-0015)
		VM:            vmRunnerOrZero(platformFlags.vmEnable),                                      // Faz 12: sanallaştırma yönetimi (opt-in; ADR-0016)
		Backup:        backupRunnerOrZero(platformFlags.backupEnable, platformFlags.backupSources), // Faz 14: restic yedeği (opt-in; ADR-0018)
		Update:        updateRunnerOrZero(platformFlags.updateEnable, platformFlags.updateTrust),   // Faz 15: imzalı güncelleme (opt-in; ADR-0020)
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

// tlsRunnerOrZero wires the Faz 09 TLS certificate runner; the family is
// OPT-IN under the same --web-enable gate (real key generation, cert
// staging, nginx reload — ADR-0013). The private key never leaves here.
func tlsRunnerOrZero(enabled bool) broker.TLSRunner {
	if !enabled {
		return nil
	}
	return tlsops.NewRunner()
}

// dbRunnerOrZero wires the Faz 10 database runner; the family is OPT-IN
// (default OFF) because it performs real database mutations — create/drop
// databases and users, grants, backups/restores (ADR-0014). Only
// node-local unix sockets are ever touched.
func dbRunnerOrZero(enabled bool) broker.DBRunner {
	if !enabled {
		return nil
	}
	return &dbops.Runner{}
}

// fsRunnerOrZero wires the Faz 10 scoped file-manager runner; OPT-IN
// (default OFF) — real file writes and ownership changes inside site
// docroots (ADR-0014). Roots are derived ONLY from webops site names.
func fsRunnerOrZero(enabled bool) broker.FsRunner {
	if !enabled {
		return nil
	}
	return &fsops.Runner{}
}

// mailRunnerOrZero wires the Faz 11 mail runner; OPT-IN (default OFF) —
// real config mutations with reloads (ADR-0015). The module never installs
// packages; mail.preflight only reports.
func mailRunnerOrZero(enabled bool) broker.MailRunner {
	if !enabled {
		return nil
	}
	return &mailops.Runner{}
}

// vmRunnerOrZero wires the Faz 12 VM runner; OPT-IN (default OFF) — real
// libvirt domain/disk/snapshot mutations (ADR-0016). Disk image paths are
// derived on the node; the module never installs libvirt.
func vmRunnerOrZero(enabled bool) broker.VMRunner {
	if !enabled {
		return nil
	}
	return &vmops.Runner{}
}

// updateRunnerOrZero wires the Faz 15 signed-update runner; OPT-IN (default
// OFF). The trust key is REQUIRED: without it every op fails closed
// (ErrTrustKeyMissing) — unsigned updates are structurally impossible
// (ADR-0020 criterion 2).
func updateRunnerOrZero(enabled bool, trustKeys string) broker.UpdateRunner {
	if !enabled {
		return nil
	}
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(trustKeys, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, err := updateops.ParseTrustKey(part)
		if err != nil {
			return nil
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil // no trust root → family structurally disabled
	}
	return updateops.NewRunner(keys)
}

// backupRunnerOrZero wires the Faz 14 restic runner; OPT-IN (default OFF) —
// real repository/snapshot mutations (ADR-0018). The repo path and the
// password file are node-managed constants; --backup-source only narrows the
// allowlisted source roots (the payload can never widen them).
func backupRunnerOrZero(enabled bool, sources string) broker.BackupRunner {
	if !enabled {
		return nil
	}
	r := &backupops.Runner{}
	if strings.TrimSpace(sources) != "" {
		var roots []string
		for _, part := range strings.Split(sources, ",") {
			part = strings.TrimSpace(part)
			if part == "" || !strings.HasPrefix(part, "/") || strings.Contains(part, "..") {
				continue
			}
			roots = append(roots, part)
		}
		if len(roots) > 0 {
			r.Sources = roots
		}
	}
	return r
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
