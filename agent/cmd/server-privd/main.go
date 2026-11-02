// Command server-privd is the local privileged broker (Faz 04/05). It runs
// with elevated privileges (root on Linux, LocalSystem on Windows), listens
// ONLY on a platform-local endpoint (mode-0660 Unix socket / restricted-SDDL
// named pipe), verifies peer identity against an explicit allowlist
// (SO_PEERCRED UIDs / impersonation SIDs), executes ONLY the typed
// allowlisted service operations (argv or SCM API — never shell strings) and
// writes one JSONL audit line per handled request.
//
// It has NO network listeners: the agent dials outbound to the control plane
// and forwards validated requests to the broker over the local endpoint.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/agent"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
)

func main() {
	var (
		auditFile = flag.String("audit-file", defaultAuditPath(), "audit JSONL dosyası")
		maxConc   = flag.Int("max-concurrent", 4, "aynı anda çalışacak işlem sayısı")
		timeoutS  = flag.Int("timeout-seconds", 60, "işlem başına zaman aşımı (saniye)")
		showVer   = flag.Bool("version", false, "sürümü yaz ve çık")
	)
	registerPlatformFlags()
	flag.Parse()
	if *showVer {
		fmt.Println("server-privd", agent.Version)
		return
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger, daemonConfig{
		auditFile: *auditFile, maxConc: *maxConc, timeout: *timeoutS,
	}); err != nil {
		logger.Error("server-privd çıktı", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// daemonConfig is the platform-neutral daemon configuration.
type daemonConfig struct {
	auditFile string
	maxConc   int
	timeout   int
}

// platformFlags carries the per-OS flag values (daemon_unix/daemon_windows).
var platformFlags struct {
	endpoint      string
	identity      string // --allow-uid (unix) / --allow-sid (windows)
	group         string // unix only
	sddl          string // windows only
	dockerSocket  string // unix only (Faz 07)
	webEnable     bool   // unix only (Faz 08): web hosting ailesi opt-in
	dbEnable      bool   // unix only (Faz 10): veritabanı ailesi opt-in
	fsEnable      bool   // unix only (Faz 10): scoped dosya yöneticisi opt-in
	mailEnable    bool   // unix only (Faz 11): e-posta ailesi opt-in
	vmEnable      bool   // unix only (Faz 12): sanallaştırma ailesi opt-in
	backupEnable  bool   // unix only (Faz 14): yedekleme ailesi opt-in
	backupSources string // unix only (Faz 14): virgüllü yedek kaynak kökleri
	updateEnable  bool   // unix only (Faz 15): imzalı güncelleme ailesi opt-in
	updateTrust   string // unix only (Faz 15): virgüllü base64 ed25519 güven kökleri
}

func auditSinkOrDie(logger *slog.Logger, path string) (*broker.JSONLSink, error) {
	sink, err := broker.NewJSONLSink(path)
	if err != nil {
		return nil, fmt.Errorf("audit dosyası açılamadı %s: %w", path, err)
	}
	return sink, nil
}

func timeoutDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}
