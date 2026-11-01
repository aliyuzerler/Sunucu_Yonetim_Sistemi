//go:build unix

package broker

import (
	"context"
	"log/slog"
	"time"

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
