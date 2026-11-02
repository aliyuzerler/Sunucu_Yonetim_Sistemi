// Package vmops implements the Faz 12 KVM/QEMU/libvirt virtualization
// adapter (VM lifecycle, disk, snapshot and console METADATA), executed
// ONLY in the privileged broker (server-privd, --vm-enable) with fixed
// argv — never a shell.
//
// Security model (ADR-0016, docs/architecture/virtualization.md):
//   - OPT-IN family (default OFF): vm.preflight only REPORTS host
//     virtualization capability (KVM/CPU flags, memory, storage pools,
//     bridges); the module NEVER installs packages and never enables
//     libvirt (criterion 1).
//   - NO disk image path ever crosses the payload boundary: callers name
//     a POOL and a managed volume; paths are derived from libvirt's own
//     output. Host mounts (losetup/guestfish/qemu-nbd) are structurally
//     absent — the adapter has no code path that mounts a guest image on
//     the host (criterion 3).
//   - Managed volume namespace: every volume this adapter creates is
//     named "sc-<vm>-*" inside a libvirt storage pool. vm.delete removes
//     ONLY those volumes and refuses anything else with a typed error
//     (ErrForeignVolume) — the VM is left defined and the state unchanged.
//   - Snapshots are INTERNAL libvirt snapshots; they require qcow2. The
//     real disk format is verified with `qemu-img info` (never trusted
//     from the XML) before create/revert; raw disks are a typed refusal
//     (ErrDiskFormat) — criterion 4 is enforced by the runner, not
//     promised in docs.
//   - Resource limits are closed ranges (vCPU 1..64, memory 128 MiB ..
//     256 GiB, disk 1..4096 GiB) validated at BOTH the control plane and
//     the broker; create additionally refuses requests the host cannot
//     satisfy (memory vs /proc/meminfo, pool free space) — criterion 2.
//   - Creating a VM does NOT manage the guest OS (criterion 5): there is
//     no in-guest agent. vm.info reports guest_agent presence honestly;
//     in-guest modules stay unsupported until an agent is installed
//     inside the guest — the UI states this explicitly.
package vmops

import (
	"errors"
	"regexp"
)

// Operation names (mirror backend/internal/tasks ops_faz12.go).
const (
	OpVMPreflight      = "vm.preflight"
	OpVMStatus         = "vm.status"
	OpVMList           = "vm.list"
	OpVMInfo           = "vm.info"
	OpVMCreate         = "vm.create"
	OpVMClone          = "vm.clone"
	OpVMStart          = "vm.start"
	OpVMStop           = "vm.stop"
	OpVMReboot         = "vm.reboot"
	OpVMDelete         = "vm.delete"
	OpVMDiskList       = "vm.disk.list"
	OpVMDiskAdd        = "vm.disk.add"
	OpVMSnapshotList   = "vm.snapshot.list"
	OpVMSnapshotCreate = "vm.snapshot.create"
	OpVMSnapshotRevert = "vm.snapshot.revert"
	OpVMSnapshotDelete = "vm.snapshot.delete"
)

// Bounds (mirrored in backend/internal/tasks ops_faz12.go).
const (
	MaxNameLen      = 32 // domain/volume/snapshot label length
	MaxPoolLen      = 48
	MaxBridgeLen    = 15 // Linux ifname limit
	MaxDescLen      = 200
	MinVCPU         = 1
	MaxVCPU         = 64
	MinMemMiB       = 128
	MaxMemMiB       = 256 << 10 // 256 GiB payload ceiling (host check is stricter)
	MinDiskGiB      = 1
	MaxDiskGiB      = 4096
	MaxList         = 200
	MaxPayloadBytes = 8 << 10

	// VolumePrefix namespaces every volume this adapter creates inside a
	// libvirt storage pool. vm.delete refuses to remove anything else.
	VolumePrefix = "sc-"

	// StopGraceSeconds bounds the graceful shutdown wait before the runner
	// reports a typed conflict (force stays an explicit caller decision).
	StopGraceSeconds = 60
)

// Managed paths (defaults; overridable per Runner for hermetic tests).
const (
	DefaultURI       = "qemu:///system"
	DefaultCPUInfo   = "/proc/cpuinfo"
	DefaultMemInfo   = "/proc/meminfo"
	DefaultKVMDevice = "/dev/kvm"
	DefaultSysNet    = "/sys/class/net"
	DefaultBaseDir   = "/etc/server-control/vm"
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidName    = errors.New("geçersiz VM/snapshot adı")
	ErrInvalidPool    = errors.New("geçersiz storage pool adı")
	ErrInvalidBridge  = errors.New("geçersiz köprü/ağ adı")
	ErrInvalidLimits  = errors.New("kaynak limitleri aralık dışı")
	ErrInsufficient   = errors.New("host kaynakları yetersiz")
	ErrNotFound       = errors.New("kayıt bulunamadı")
	ErrExists         = errors.New("kayıt zaten var")
	ErrConflict       = errors.New("VM durumu çelişkili")
	ErrDiskFormat     = errors.New("disk formatı bu işlemi desteklemiyor (yalnız qcow2)")
	ErrFormatMismatch = errors.New("disk formatı XML ile gerçek biçim arasında uyuşmuyor")
	ErrForeignVolume  = errors.New("yabancı disk birimi — yalnız sc- önekli yönetilen birimler silinir")
	ErrUnavailable    = errors.New("libvirt kullanılamıyor")
	ErrLimit          = errors.New("sınır aşıldı")
)

// nameRE: strict lowercase DNS-label allowlist for domain/snapshot names.
// Anything it rejects never reaches a virsh argv or a domain XML file.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// poolRE: libvirt pool names are more permissive, but the managed subset
// stays strict (no slash, no leading dot — traversal-proof).
var poolRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,47}$`)

// bridgeRE: Linux interface name rules (<=15 chars, no slash/space).
var bridgeRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)

// ValidVMName reports whether the domain/snapshot name passes the
// allowlist (lowercase DNS label; traversal and injection impossible).
func ValidVMName(n string) bool { return nameRE.MatchString(n) }

// ValidPoolName reports whether the pool name passes the allowlist.
func ValidPoolName(n string) bool { return poolRE.MatchString(n) }

// ValidBridge reports whether the bridge/network name passes the allowlist.
func ValidBridge(n string) bool { return bridgeRE.MatchString(n) }

// ValidVCPU reports whether the vCPU count is inside the closed range.
func ValidVCPU(v int64) bool { return v >= MinVCPU && v <= MaxVCPU }

// ValidMemMiB reports whether the memory (MiB) is inside the closed range.
func ValidMemMiB(v int64) bool { return v >= MinMemMiB && v <= MaxMemMiB }

// ValidDiskGiB reports whether the disk size (GiB) is inside the closed range.
func ValidDiskGiB(v int64) bool { return v >= MinDiskGiB && v <= MaxDiskGiB }

// ValidDescription bounds the free-text snapshot description (it is
// XML-escaped by the renderer anyway; the bound keeps payloads tiny).
func ValidDescription(d string) bool { return len(d) <= MaxDescLen }

// ManagedVolume reports whether a volume NAME is in the managed namespace.
// Callers apply it to the BASENAME of a source path (the runner splits).
func ManagedVolume(name string) bool {
	if len(name) < len(VolumePrefix)+1 || len(name) > MaxNameLen+len(VolumePrefix)+8 {
		return false
	}
	return name[:len(VolumePrefix)] == VolumePrefix && nameRE.MatchString(name[len(VolumePrefix):])
}

// PoolInfo is one libvirt storage pool (preflight + create preconditions).
type PoolInfo struct {
	Name          string `json:"name"`
	Type          string `json:"type"` // dir, logical, zfs, ...
	Active        bool   `json:"active"`
	CapacityBytes int64  `json:"capacity_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
	TargetPath    string `json:"target_path,omitempty"`
}

// PreflightResult reports host virtualization capability WITHOUT changing
// anything (criterion 1): KVM/CPU flags, memory, storage pools, bridges
// and the libvirt tooling contract.
type PreflightResult struct {
	KVMAvailable bool       `json:"kvm_available"`
	CPUModel     string     `json:"cpu_model,omitempty"`
	CPUFlags     []string   `json:"cpu_flags,omitempty"`
	VirtFlag     string     `json:"virt_flag,omitempty"` // vmx | svm | ""
	MemTotalMiB  int64      `json:"mem_total_mib"`
	MemAvailMiB  int64      `json:"mem_available_mib"`
	LibvirtVer   string     `json:"libvirt_version,omitempty"`
	Hypervisor   string     `json:"hypervisor,omitempty"`
	URI          string     `json:"uri"`
	QemuImg      bool       `json:"qemu_img_available"`
	Pools        []PoolInfo `json:"pools,omitempty"`
	Bridges      []string   `json:"bridges,omitempty"`
	Networks     []string   `json:"networks,omitempty"`
	Notes        []string   `json:"notes,omitempty"`
}

// StatusResult summarises the libvirt daemon contract (vm.status).
type StatusResult struct {
	URI          string `json:"uri"`
	LibvirtVer   string `json:"libvirt_version,omitempty"`
	Hypervisor   string `json:"hypervisor,omitempty"`
	DomainsTotal int    `json:"domains_total"`
	Running      int    `json:"running"`
	KVMAvailable bool   `json:"kvm_available"`
}

// VMRow is one domain summary (vm.list).
type VMRow struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Running bool   `json:"running"`
}

// DiskInfo is one disk of a domain with VERIFIED format facts
// (criterion 3/4): the XML-claimed driver type is compared against the
// real `qemu-img info` detection; format_verified=false means the image
// lies about its format and mutation paths will refuse it.
type DiskInfo struct {
	TargetDev      string `json:"target_dev"`
	SourcePath     string `json:"source_path,omitempty"`
	Pool           string `json:"pool,omitempty"`
	Volume         string `json:"volume,omitempty"`
	Managed        bool   `json:"managed"`
	XMLFormat      string `json:"xml_format,omitempty"`
	ActualFormat   string `json:"actual_format,omitempty"`
	FormatVerified bool   `json:"format_verified"`
	VirtualBytes   int64  `json:"virtual_bytes"`
	ActualBytes    int64  `json:"actual_bytes"`
	CheckOK        bool   `json:"check_ok"`
	CheckDetail    string `json:"check_detail,omitempty"`
}

// NicInfo is one network interface (metadata only).
type NicInfo struct {
	MAC     string `json:"mac,omitempty"`
	Bridge  string `json:"bridge,omitempty"`
	Network string `json:"network,omitempty"`
	Model   string `json:"model,omitempty"`
}

// ConsoleMeta is console METADATA (vm.info) — type/port/listen of the
// graphics device plus serial presence. The adapter NEVER proxies or
// exposes a console stream; listen is reported so operators can see the
// exposure themselves (local-only is the configured default).
type ConsoleMeta struct {
	GraphicsType string `json:"graphics_type,omitempty"`
	Listen       string `json:"listen,omitempty"`
	Port         int    `json:"port,omitempty"`
	LocalOnly    bool   `json:"local_only"`
	Serial       bool   `json:"serial"`
}

// VMInfo is the typed vm.info report.
type VMInfo struct {
	Name       string      `json:"name"`
	UUID       string      `json:"uuid,omitempty"`
	State      string      `json:"state"`
	Running    bool        `json:"running"`
	VCPU       int         `json:"vcpu"`
	MemMiB     int64       `json:"memory_mib"`
	Arch       string      `json:"arch,omitempty"`
	GuestAgent bool        `json:"guest_agent"` // criterion 5: honest in-guest signal
	Disks      []DiskInfo  `json:"disks,omitempty"`
	Interfaces []NicInfo   `json:"interfaces,omitempty"`
	Console    ConsoleMeta `json:"console"`
}

// CreateResult is the typed vm.create outcome.
type CreateResult struct {
	Name        string `json:"name"`
	UUID        string `json:"uuid"`
	VCPU        int    `json:"vcpu"`
	MemMiB      int64  `json:"memory_mib"`
	DiskGiB     int64  `json:"disk_gib"`
	Pool        string `json:"pool"`
	Volume      string `json:"volume"`
	Bridge      string `json:"bridge"`
	BridgeKind  string `json:"bridge_kind"` // bridge | network
	Accelerator string `json:"accelerator"` // kvm | qemu (TCG)
	State       string `json:"state"`
}

// CloneResult is the typed vm.clone outcome (stopped source only).
type CloneResult struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Volume string `json:"volume"`
}

// MutateResult is the shared start/reboot outcome. Already=true reports an
// honest no-op when the desired state was already satisfied.
type MutateResult struct {
	Name    string `json:"name"`
	Action  string `json:"action"`
	Already bool   `json:"already"`
	State   string `json:"state,omitempty"`
}

// StopResult is the typed vm.stop outcome. Forced=true means a destroy
// (power-cut) was executed because the caller explicitly asked for it;
// graceful_timed_out=true reports that ACPI shutdown did not converge in
// the bounded window (the runner never escalates on its own).
type StopResult struct {
	Name             string `json:"name"`
	Already          bool   `json:"already"`
	Forced           bool   `json:"forced"`
	GracefulTimedOut bool   `json:"graceful_timed_out"`
	State            string `json:"state,omitempty"`
}

// DeleteResult is the typed vm.delete outcome; it lists exactly which
// managed volumes were removed (criterion 3 auditability).
type DeleteResult struct {
	Name           string   `json:"name"`
	Undefined      bool     `json:"undefined"`
	VolumesRemoved []string `json:"volumes_removed,omitempty"`
}

// DiskAddResult is the typed vm.disk.add outcome.
type DiskAddResult struct {
	Name      string `json:"name"`
	Pool      string `json:"pool"`
	Volume    string `json:"volume"`
	TargetDev string `json:"target_dev"`
	SizeGiB   int64  `json:"size_gib"`
}

// SnapshotRow is one snapshot summary (vm.snapshot.list).
type SnapshotRow struct {
	Name         string `json:"name"`
	Current      bool   `json:"current"`
	CreationTime string `json:"creation_time,omitempty"`
	State        string `json:"state,omitempty"`
}

// SnapshotCreateResult is the typed vm.snapshot.create outcome; the
// verified disk formats are echoed back so the caller can audit the
// qcow2 gate (criterion 4).
type SnapshotCreateResult struct {
	Name            string   `json:"name"`
	Domain          string   `json:"domain"`
	VerifiedFormats []string `json:"verified_formats,omitempty"`
}
