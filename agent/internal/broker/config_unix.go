//go:build unix

package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
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
	// Faz 07: Docker Engine runner (nil = docker family rejected as
	// unsupported; capabilities report unavailable). Socket path is
	// DockerSocket; empty defaults to /var/run/docker.sock.
	Docker       DockerRunner
	DockerSocket string
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

// DockerRunner executes the Faz 07 Docker Engine operations over the local
// unix socket. *dockerx.Client implements it. PREVIEW-ONLY invariant
// (ADR-0011): the interface has NO remove/delete method — the remove family
// never executes (rendered as data by dockerx.RenderRemovePreview).
type DockerRunner interface {
	Ping(ctx context.Context) (dockerx.PingInfo, error)
	ContainersList(ctx context.Context, limit int) ([]dockerx.ContainerSummary, error)
	ContainerInspect(ctx context.Context, id string) (dockerx.ContainerDetail, error)
	ContainerLogs(ctx context.Context, id string, tail int) (dockerx.LogChunk, error)
	ContainerStart(ctx context.Context, id string) error
	ContainerStop(ctx context.Context, id string, timeout int) error
	ContainerRestart(ctx context.Context, id string, timeout int) error
	ImagesList(ctx context.Context, limit int) ([]dockerx.ImageSummary, error)
	NetworksList(ctx context.Context, limit int) ([]dockerx.NetworkSummary, error)
	VolumesList(ctx context.Context, limit int) ([]dockerx.VolumeInfo, error)
}

// compile-time: the real client satisfies the runner surface.
var _ DockerRunner = (*dockerx.Client)(nil)
