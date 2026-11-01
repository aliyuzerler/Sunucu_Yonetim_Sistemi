//go:build unix

package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
)

// Config is the unix broker daemon configuration.
type Config struct {
	// AllowedUIDs is the explicit SO_PEERCRED UID allowlist; empty = deny all.
	AllowedUIDs []uint32
	// MaxConcurrent bounds parallel executions (default 4).
	MaxConcurrent int
	// ExecTimeout bounds per-op duration (default 60s).
	ExecTimeout time.Duration
	// Systemd runs linux.systemd.service.* operations.
	Systemd SystemdRunner
	// Faz 06: read-only package inventory (linux.pkg.list).
	Pkg PkgRunner
	// Faz 06: firewall survey + preview-only rendering.
	Firewall FirewallRunner
	// Audit receives one entry per handled request (incl. rejections).
	Audit  AuditSink
	Logger *slog.Logger
}

// SystemdRunner executes one linux systemd service operation.
// *svcops.Manager implements it; the interface keeps tests hermetic.
type SystemdRunner interface {
	List(ctx context.Context) ([]svcops.UnitInfo, error)
	Status(ctx context.Context, unit string) (svcops.UnitInfo, error)
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
}

// PkgRunner executes the read-only package inventory.
// *pkgops.Runner implements it; the interface keeps tests hermetic.
type PkgRunner interface {
	List(ctx context.Context, limit int) ([]pkgops.PackageInfo, error)
}

// FirewallRunner detects the active backend and lists rules (read-only).
// *fwops.Runner implements it; the interface keeps tests hermetic. Preview
// rendering is a PURE function (fwops.RenderPreview) and needs no runner.
type FirewallRunner interface {
	Probe(ctx context.Context) fwops.Status
	Rules(ctx context.Context, limit int) (fwops.Ruleset, error)
}
