//go:build unix

package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dbops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/dockerx"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/fwops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/mailops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/pkgops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/svcops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/tlsops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/vmops"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/webops"
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
	// Faz 08: web hosting runner (nil = web family rejected as unsupported;
	// the daemon enables it ONLY with --web-enable — ADR-0012: real user/
	// config mutations are opt-in).
	Web WebRunner
	// Faz 09: TLS sertifika runner (nil = cert ailesi reddedilir; web
	// ailesiyle aynı --web-enable kapısı — ADR-0013).
	TLS TLSRunner
	// Faz 10: veritabanı runner'ı (nil = db ailesi tipli red; --db-enable
	// ile seçilir, VARSAYILAN KAPALI — ADR-0014).
	DB DBRunner
	// Faz 10: scoped dosya yöneticisi runner'ı (nil = fs ailesi tipli red;
	// --fs-enable ile seçilir, VARSAYILAN KAPALI — ADR-0014).
	Fs FsRunner

	// Faz 11: e-posta servisi runner'ı (nil = mail ailesi tipli red;
	// --mail-enable ile seçilir, VARSAYILAN KAPALI — ADR-0015).
	Mail MailRunner

	// Faz 12: VM runner'ı (nil = vm ailesi tipli red; --vm-enable ile
	// seçilir, VARSAYILAN KAPALI — ADR-0016). Disk yolu payload'da yoktur.
	VM VMRunner

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

// WebRunner executes the Faz 08 web hosting operations (nginx + PHP-FPM
// site lifecycle). *webops.Runner implements it. The interface keeps broker
// tests hermetic; the runner performs REAL mutations (useradd, atomic config
// writes, reloads) and is wired only behind --web-enable (ADR-0012).
type WebRunner interface {
	List(ctx context.Context, limit int) ([]webops.SiteInfo, error)
	Status(ctx context.Context, site string) (webops.SiteStatus, error)
	Create(ctx context.Context, spec webops.SiteSpec) (webops.SiteResult, error)
	Update(ctx context.Context, spec webops.SiteSpec) (webops.SiteResult, error)
	Disable(ctx context.Context, site string) (webops.SiteResult, error)
	Delete(ctx context.Context, site string) (webops.SiteResult, error)
	PHPVersions(ctx context.Context) (webops.PHPMatrix, error)
}

// compile-time: the real runner satisfies the interface.
var _ WebRunner = (*webops.Runner)(nil)

// TLSRunner executes the Faz 09 node-side TLS certificate operations.
// *tlsops.Runner implements it. The private key NEVER leaves the runner
// (only CSRs and public certificate material cross the boundary).
type TLSRunner interface {
	Request(ctx context.Context, domain string, sans []string) (tlsops.RequestResult, error)
	Install(ctx context.Context, domain, certPEM, chainPEM string) (tlsops.InstallResult, error)
	Status(ctx context.Context) ([]tlsops.CertStatus, error)
}

// compile-time: the real runner satisfies the interface.
var _ TLSRunner = (*tlsops.Runner)(nil)

// DBRunner executes the Faz 10 database operations against LOCAL instances
// (node-local unix sockets only; the payload schema has no host/port fields
// — the control plane's own PostgreSQL is structurally out of reach,
// ADR-0014). *dbops.Runner implements it; the interface keeps tests hermetic.
type DBRunner interface {
	InstancesList(ctx context.Context) ([]dbops.Instance, error)
	DatabasesList(ctx context.Context, kind string, limit int) ([]dbops.DatabaseInfo, error)
	DatabaseCreate(ctx context.Context, kind, name, owner string) (map[string]any, error)
	DatabaseDelete(ctx context.Context, kind, name string) (map[string]any, error)
	UsersList(ctx context.Context, kind string, limit int) ([]dbops.UserInfo, error)
	UserCreate(ctx context.Context, kind, name, host string) (dbops.UserCreateResult, error)
	UserDelete(ctx context.Context, kind, name, host string) (map[string]any, error)
	GrantSet(ctx context.Context, kind, user, host, database, level string) (map[string]any, error)
	Backup(ctx context.Context, kind, database string) (map[string]any, error)
	BackupsList(ctx context.Context, kind string, limit int) ([]dbops.BackupInfo, error)
	Restore(ctx context.Context, kind, database, backup string) (map[string]any, error)
}

// compile-time: the real runner satisfies the interface.
var _ DBRunner = (*dbops.Runner)(nil)

// FsRunner executes the Faz 10 scoped file-manager operations (roots are
// webops site docroots only; openat+O_NOFOLLOW race-free traversal,
// ADR-0014). *fsops.Runner implements it.
type FsRunner interface {
	List(ctx context.Context, root, path string, limit int) (fsops.ListResult, error)
	Download(ctx context.Context, root, path string) (fsops.DownloadResult, error)
	Upload(ctx context.Context, root, path, content string, mode uint32, overwrite bool) (fsops.UploadResult, error)
	Chmod(ctx context.Context, root, path string, mode uint32) (fsops.ChmodResult, error)
	Chown(ctx context.Context, root, path string) (fsops.ChownResult, error)
}

// compile-time: the real runner satisfies the interface.
var _ FsRunner = (*fsops.Runner)(nil)

// MailRunner executes the Faz 11 e-mail service operations (Postfix +
// Dovecot + Rspamd). *mailops.Runner implements it. Every mutation is
// syntax-gated with rollback inside the runner (ADR-0015 criterion 4);
// the interface keeps broker tests hermetic.
type MailRunner interface {
	Preflight(ctx context.Context) (mailops.PreflightResult, error)
	Status(ctx context.Context) (mailops.StatusResult, error)
	DomainList(ctx context.Context, limit int) ([]mailops.DomainInfo, error)
	DomainCreate(ctx context.Context, domain string) (mailops.MutateResult, error)
	DomainDelete(ctx context.Context, domain string) (mailops.MutateResult, error)
	MailboxList(ctx context.Context, domain string, limit int) ([]mailops.MailboxInfo, error)
	MailboxCreate(ctx context.Context, domain, local string) (map[string]any, error)
	MailboxDelete(ctx context.Context, address string) (map[string]any, error)
	AliasList(ctx context.Context, domain string, limit int) ([]mailops.AliasInfo, error)
	AliasCreate(ctx context.Context, alias, target string) (mailops.MutateResult, error)
	AliasDelete(ctx context.Context, alias string) (mailops.MutateResult, error)
	QuotaSet(ctx context.Context, address string, quotaBytes int64) (mailops.MutateResult, error)
	ConfigCheck(ctx context.Context) (mailops.ConfigCheckResult, error)
	QueueList(ctx context.Context, limit int) ([]mailops.QueueEntry, error)
	LimitsGet(ctx context.Context) (mailops.LimitsGetResult, error)
	LimitsSet(ctx context.Context, limits map[string]int64) (mailops.LimitsSetResult, error)
}

// compile-time: the real runner satisfies the interface.
var _ MailRunner = (*mailops.Runner)(nil)

// VMRunner executes the Faz 12 KVM/QEMU/libvirt virtualization operations
// (fixed argv virsh/qemu-img; disk image paths NEVER cross the payload —
// ADR-0016). *vmops.Runner implements it; the interface keeps broker
// tests hermetic.
type VMRunner interface {
	Preflight(ctx context.Context) (vmops.PreflightResult, error)
	Status(ctx context.Context) (vmops.StatusResult, error)
	List(ctx context.Context, limit int) ([]vmops.VMRow, error)
	Info(ctx context.Context, name string) (vmops.VMInfo, error)
	Create(ctx context.Context, name string, vcpu int, memMiB, diskGiB int64, pool, bridge string) (vmops.CreateResult, error)
	Clone(ctx context.Context, src, name string) (vmops.CloneResult, error)
	Start(ctx context.Context, name string) (vmops.MutateResult, error)
	Stop(ctx context.Context, name string, force bool) (vmops.StopResult, error)
	Reboot(ctx context.Context, name string) (vmops.MutateResult, error)
	Delete(ctx context.Context, name string) (vmops.DeleteResult, error)
	DiskList(ctx context.Context, name string) ([]vmops.DiskInfo, error)
	DiskAdd(ctx context.Context, name, pool string, sizeGiB int64) (vmops.DiskAddResult, error)
	SnapshotList(ctx context.Context, name string) ([]vmops.SnapshotRow, error)
	SnapshotCreate(ctx context.Context, domain, snap, description string) (vmops.SnapshotCreateResult, error)
	SnapshotRevert(ctx context.Context, domain, snap string) (vmops.MutateResult, error)
	SnapshotDelete(ctx context.Context, domain, snap string) (vmops.MutateResult, error)
}

// compile-time: the real runner satisfies the interface.
var _ VMRunner = (*vmops.Runner)(nil)
