//go:build windows

package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// Config is the windows broker daemon configuration.
type Config struct {
	// AllowedSIDs is the explicit caller SID allowlist (canonical "S-1-…");
	// empty = deny all.
	AllowedSIDs []string
	// MaxConcurrent bounds parallel executions (default 4).
	MaxConcurrent int
	// ExecTimeout bounds per-op duration (default 60s).
	ExecTimeout time.Duration
	// Scm runs windows.scm.service.* operations.
	Scm ScmRunner
	// Audit receives one entry per handled request (incl. rejections).
	Audit  AuditSink
	Logger *slog.Logger
}

// ScmRunner executes one windows SCM service operation. *winscm.Manager
// implements it; the interface keeps tests hermetic.
type ScmRunner interface {
	List(ctx context.Context) ([]winops.ServiceInfo, error)
	Status(ctx context.Context, name string) (winops.ServiceInfo, error)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
}
