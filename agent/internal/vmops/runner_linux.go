//go:build linux

// Runner implementation for the Faz 12 KVM/QEMU/libvirt adapter. EVERY
// external call runs a fixed program with a fixed argv (no shell, no
// interpolation beyond allowlist-validated identifiers).
//
// Invariants (ADR-0016):
//   - preflight is report-only; nothing is installed or enabled;
//   - disk image PATHS never come from the payload — they are resolved
//     from libvirt's own output (dumpxml/vol-path) and used only as
//     arguments to virsh/qemu-img (never mounted on the host);
//   - only volumes named sc-<vm>-* inside libvirt pools are removed;
//   - snapshot create/revert verify the REAL image format via qemu-img
//     (qcow2-only gate; raw is a typed refusal);
//   - graceful stop never escalates to destroy on its own.
package vmops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner executes VM operations on the node. Tool paths, the libvirt URI
// and proc/sys paths are injectable for hermetic tests; the zero value
// looks binaries up on PATH and uses the documented defaults.
type Runner struct {
	Virsh   string // virsh ("" → LookPath)
	QemuImg string // qemu-img ("" → LookPath)
	URI     string // default qemu:///system

	CPUInfoPath string // default /proc/cpuinfo
	MemInfoPath string // default /proc/meminfo
	KVMPath     string // default /dev/kvm
	SysNetPath  string // default /sys/class/net
	BaseDir     string // XML staging dir (default /etc/server-control/vm)

	Timeout   time.Duration // per external command (default 120s)
	StopGrace time.Duration // graceful shutdown window (default 60s)
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 120 * time.Second
}

func (r *Runner) stopGrace() time.Duration {
	if r.StopGrace > 0 {
		return r.StopGrace
	}
	return StopGraceSeconds * time.Second
}

func (r *Runner) uri() string {
	if r.URI != "" {
		return r.URI
	}
	return DefaultURI
}

func (r *Runner) cpuInfo() string {
	if r.CPUInfoPath != "" {
		return r.CPUInfoPath
	}
	return DefaultCPUInfo
}

func (r *Runner) memInfo() string {
	if r.MemInfoPath != "" {
		return r.MemInfoPath
	}
	return DefaultMemInfo
}

func (r *Runner) kvmPath() string {
	if r.KVMPath != "" {
		return r.KVMPath
	}
	return DefaultKVMDevice
}

func (r *Runner) sysNet() string {
	if r.SysNetPath != "" {
		return r.SysNetPath
	}
	return DefaultSysNet
}

func (r *Runner) baseDir() string {
	if r.BaseDir != "" {
		return r.BaseDir
	}
	return DefaultBaseDir
}

// resolve looks a tool up (injectable path wins).
func resolve(injected, name string) (string, bool) {
	if injected != "" {
		return injected, true
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// runArgv executes one fixed program with an argument vector (no shell).
// stderr detail is intentionally NOT returned (log safety); errors carry a
// short program context only.
func runArgv(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + argv, no user shell
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		// Bounded, single-line stderr tail — the libvirt/qemu-img parse
		// errors are otherwise invisible in CI (CI-37 diagnostics lesson).
		// virsh/qemu-img errors carry config context only (no secrets).
		detail := strings.Join(strings.Fields(stderr.String()), " ")
		if len(detail) > 200 {
			detail = detail[len(detail)-200:]
		}
		if detail != "" {
			return nil, fmt.Errorf("%s başarısız: %w: %s", filepath.Base(bin), err, detail)
		}
		return nil, fmt.Errorf("%s başarısız: %w", filepath.Base(bin), err)
	}
	return stdout.Bytes(), nil
}

// virsh runs one virsh command against the configured URI (quiet mode).
// A configured-but-missing binary is the same honest unavailability as a
// failed lookup (the tool can vanish at runtime; we never guess).
func (r *Runner) virsh(ctx context.Context, args ...string) ([]byte, error) {
	bin, ok := resolve(r.Virsh, "virsh")
	if !ok {
		return nil, ErrUnavailable
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, ErrUnavailable
	}
	full := append([]string{"-q", "--connect", r.uri()}, args...)
	return runArgv(ctx, bin, full...)
}

// virshOK reports whether a virsh command succeeds.
func (r *Runner) virshOK(ctx context.Context, args ...string) bool {
	_, err := r.virsh(ctx, args...)
	return err == nil
}

// qemuImgBin resolves qemu-img.
func (r *Runner) qemuImgBin() (string, bool) {
	return resolve(r.QemuImg, "qemu-img")
}

// kvmAvailable reports the nested-virtualization truth: a CPU virt flag
// (vmx/svm) AND a present /dev/kvm device.
func (r *Runner) kvmAvailable() (bool, string) {
	if _, _, virt := readCPUInfo(r.cpuInfo()); virt != "" {
		if _, err := os.Stat(r.kvmPath()); err == nil {
			return true, virt
		}
	}
	return false, ""
}

// ---------- host fact readers (pure, injectable paths) ----------

// readCPUInfo parses the first processor block: model name, full flags and
// the virtualization flag (vmx | svm | "").
func readCPUInfo(path string) (model string, flags []string, virt string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "model name"):
			if model == "" {
				if _, rest, ok := strings.Cut(line, ":"); ok {
					model = strings.TrimSpace(rest)
				}
			}
		case strings.HasPrefix(line, "flags"):
			if _, rest, ok := strings.Cut(line, ":"); ok {
				flags = strings.Fields(rest)
			}
		}
	}
	for _, f := range flags {
		if f == "vmx" || f == "svm" {
			virt = f
			break
		}
	}
	return model, flags, virt
}

// readMemInfo returns MemTotal and MemAvailable in MiB (0 on parse miss).
func readMemInfo(path string) (totalMiB, availMiB int64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			totalMiB = kb / 1024
		case "MemAvailable":
			availMiB = kb / 1024
		}
	}
	return totalMiB, availMiB
}

// hostBridges lists /sys/class/net entries that are bridges.
func hostBridges(sysNet string) []string {
	entries, err := os.ReadDir(sysNet)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(sysNet, e.Name(), "bridge")); err == nil && st.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// ---------- libvirt primitives ----------

// exists reports whether the domain is defined.
func (r *Runner) exists(ctx context.Context, name string) bool {
	return r.virshOK(ctx, "domuuid", name)
}

// domstate returns the libvirt state string (e.g. "running", "shut off").
// Unavailability is reported as-is (never masked as not-found).
func (r *Runner) domstate(ctx context.Context, name string) (string, error) {
	out, err := r.virsh(ctx, "domstate", name)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return "", ErrUnavailable
		}
		if !r.exists(ctx, name) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("domstate okunamadı: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// listNames returns all domain names (bounded by the caller).
func (r *Runner) listNames(ctx context.Context) ([]string, error) {
	out, err := r.virsh(ctx, "list", "--all", "--name")
	if err != nil {
		return nil, fmt.Errorf("domain listesi alınamadı: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		n := strings.TrimSpace(line)
		if n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// xmlPool mirrors the parts of `virsh pool-dumpxml` we need.
type xmlPool struct {
	Name     string `xml:"name"`
	Capacity struct {
		Text int64  `xml:",chardata"`
		Unit string `xml:"unit,attr"`
	} `xml:"capacity"`
	Available struct {
		Text int64  `xml:",chardata"`
		Unit string `xml:"unit,attr"`
	} `xml:"available"`
	Target struct {
		Path string `xml:"path"`
	} `xml:"target"`
}

// listPools returns every storage pool with facts (type/size/path).
func (r *Runner) listPools(ctx context.Context) ([]PoolInfo, error) {
	allOut, err := r.virsh(ctx, "pool-list", "--all", "--name")
	if err != nil {
		return nil, fmt.Errorf("pool listesi alınamadı: %w", err)
	}
	var active map[string]bool
	if actOut, aerr := r.virsh(ctx, "pool-list", "--name"); aerr == nil {
		active = map[string]bool{}
		for _, line := range strings.Split(string(actOut), "\n") {
			if n := strings.TrimSpace(line); n != "" {
				active[n] = true
			}
		}
	}
	var pools []PoolInfo
	for _, line := range strings.Split(string(allOut), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		p := PoolInfo{Name: name, Active: active[name]}
		if xout, xerr := r.virsh(ctx, "pool-dumpxml", name); xerr == nil {
			var xp xmlPool
			if xml.Unmarshal(xout, &xp) == nil {
				p.Type = poolType(xout)
				p.CapacityBytes = unitToBytes(xp.Capacity.Text, xp.Capacity.Unit)
				p.FreeBytes = unitToBytes(xp.Available.Text, xp.Available.Unit)
				p.TargetPath = xp.Target.Path
			}
		}
		pools = append(pools, p)
	}
	return pools, nil
}

// poolType extracts the pool type attribute without a full struct dance.
func poolType(data []byte) string {
	var probe struct {
		Type string `xml:"type,attr"`
	}
	if xml.Unmarshal(data, &probe) == nil {
		return probe.Type
	}
	return ""
}

// unitToBytes normalises libvirt size units to bytes (default bytes).
func unitToBytes(v int64, unit string) int64 {
	switch unit {
	case "", "bytes":
		return v
	case "KiB":
		return v << 10
	case "MiB":
		return v << 20
	case "GiB":
		return v << 30
	default:
		return v
	}
}

// listNetworks returns libvirt network names (all states).
func (r *Runner) listNetworks(ctx context.Context) ([]string, error) {
	out, err := r.virsh(ctx, "net-list", "--all", "--name")
	if err != nil {
		return nil, fmt.Errorf("ağ listesi alınamadı: %w", err)
	}
	var nets []string
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); n != "" {
			nets = append(nets, n)
		}
	}
	return nets, nil
}

// ---------- domain XML (parse + render) ----------

type xmlDisk struct {
	Device string `xml:"device,attr"`
	Driver *struct {
		Type string `xml:"type,attr"`
	} `xml:"driver"`
	Target *struct {
		Dev string `xml:"dev,attr"`
	} `xml:"target"`
	Source *struct {
		File   string `xml:"file,attr"`
		Volume string `xml:"volume,attr"`
		Pool   string `xml:"pool,attr"`
	} `xml:"source"`
}

type xmlIface struct {
	MAC *struct {
		Address string `xml:"address,attr"`
	} `xml:"mac"`
	Source *struct {
		Bridge  string `xml:"bridge,attr"`
		Network string `xml:"network,attr"`
	} `xml:"source"`
	Model *struct {
		Type string `xml:"type,attr"`
	} `xml:"model"`
}

type xmlGfx struct {
	Type   string `xml:"type,attr"`
	Port   int    `xml:"port,attr"`
	Listen *struct {
		Text    string `xml:",chardata"`
		Address string `xml:"address,attr"`
	} `xml:"listen"`
}

type xmlChannel struct {
	Target *struct {
		Name string `xml:"name,attr"`
	} `xml:"target"`
}

type xmlDomain struct {
	Name   string `xml:"name"`
	UUID   string `xml:"uuid"`
	Memory struct {
		Text int64  `xml:",chardata"`
		Unit string `xml:"unit,attr"`
	} `xml:"memory"`
	VCPU int `xml:"vcpu"`
	OS   struct {
		Type struct {
			Arch string `xml:"arch,attr"`
		} `xml:"type"`
		NVRAM *struct {
			Text string `xml:",chardata"`
		} `xml:"nvram"`
	} `xml:"os"`
	Devices struct {
		Disks    []xmlDisk    `xml:"disk"`
		Ifaces   []xmlIface   `xml:"interface"`
		Gfx      []xmlGfx     `xml:"graphics"`
		Channels []xmlChannel `xml:"channel"`
		Consoles []struct {
			Type string `xml:"type,attr"`
		} `xml:"console"`
		Serials []struct {
			Type string `xml:"type,attr"`
		} `xml:"serial"`
	} `xml:"devices"`
}

// dumpDomain parses `virsh dumpxml` for a defined domain.
func (r *Runner) dumpDomain(ctx context.Context, name string) (*xmlDomain, error) {
	out, err := r.virsh(ctx, "dumpxml", name)
	if err != nil {
		return nil, ErrNotFound
	}
	var d xmlDomain
	if err := xml.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("domain XML çözülemedi: %w", err)
	}
	return &d, nil
}

// memToMiB normalises the domain <memory unit> to MiB.
func (d *xmlDomain) memToMiB() int64 {
	switch d.Memory.Unit {
	case "KiB", "":
		return d.Memory.Text / 1024
	case "MiB":
		return d.Memory.Text
	case "GiB":
		return d.Memory.Text << 10
	default:
		return d.Memory.Text / (1 << 20)
	}
}

// esc escapes a dynamic value for XML text/attribute context. All values
// are already allowlist-validated; escaping is defence in depth.
func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// renderDomain builds the managed libvirt domain XML. The graphics device
// is bound to 127.0.0.1 ONLY (console metadata may be inspected, never
// exposed) and a virtio guest-agent channel is always present so an
// in-guest agent CAN be installed later (criterion 5 honesty).
func (r *Runner) renderDomain(name, uuid string, vcpu int, memMiB int64, pool, volume, bridge, bridgeKind, accelerator string) string {
	var b strings.Builder
	domainType := "qemu"
	if accelerator == "kvm" {
		domainType = "kvm"
	}
	fmt.Fprintf(&b, "<domain type='%s'>\n", domainType)
	fmt.Fprintf(&b, "  <name>%s</name>\n", esc(name))
	fmt.Fprintf(&b, "  <uuid>%s</uuid>\n", esc(uuid))
	fmt.Fprintf(&b, "  <memory unit='MiB'>%d</memory>\n", memMiB)
	fmt.Fprintf(&b, "  <vcpu>%d</vcpu>\n", vcpu)
	fmt.Fprintf(&b, "  <os><type arch='x86_64' machine='pc'>hvm</type></os>\n")
	fmt.Fprintf(&b, "  <features><acpi/><apic/></features>\n")
	fmt.Fprintf(&b, "  <clock offset='utc'/>\n")
	fmt.Fprintf(&b, "  <devices>\n")
	fmt.Fprintf(&b, "    <disk type='volume' device='disk'>\n")
	fmt.Fprintf(&b, "      <driver name='qemu' type='qcow2'/>\n")
	fmt.Fprintf(&b, "      <source pool='%s' volume='%s'/>\n", esc(pool), esc(volume))
	fmt.Fprintf(&b, "      <target dev='vda' bus='virtio'/>\n")
	fmt.Fprintf(&b, "    </disk>\n")
	if bridgeKind == "network" {
		fmt.Fprintf(&b, "    <interface type='network'>\n")
		fmt.Fprintf(&b, "      <source network='%s'/>\n", esc(bridge))
	} else {
		fmt.Fprintf(&b, "    <interface type='bridge'>\n")
		fmt.Fprintf(&b, "      <source bridge='%s'/>\n", esc(bridge))
	}
	fmt.Fprintf(&b, "      <model type='virtio'/>\n")
	fmt.Fprintf(&b, "    </interface>\n")
	fmt.Fprintf(&b, "    <console type='pty'><target type='serial' port='0'/></console>\n")
	fmt.Fprintf(&b, "    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>\n")
	fmt.Fprintf(&b, "    <channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>\n")
	fmt.Fprintf(&b, "    <memballoon model='virtio'/>\n")
	fmt.Fprintf(&b, "  </devices>\n")
	b.WriteString("</domain>\n")
	return b.String()
}

// stageXML writes XML to the managed staging dir (0600) and returns the
// path; the caller removes the file after virsh consumed it.
func (r *Runner) stageXML(name, content string) (string, error) {
	dir := r.baseDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("staging dizini oluşturulamadı: %w", err)
	}
	f, err := os.CreateTemp(dir, "define-"+name+"-*.xml")
	if err != nil {
		return "", fmt.Errorf("staging dosyası yazılamadı: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("staging dosyası yazılamadı: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// newUUID generates a random RFC 4122 v4 UUID (crypto/rand).
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return h, nil
}

// newMAC generates a KVM locally-administered MAC (52:54:00 prefix).
func newMAC() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// ---------- qemu-img helpers (real format detection — criterion 4) ----------

type qemuImgInfo struct {
	VirtualSize int64  `json:"virtual-size"`
	ActualSize  int64  `json:"actual-size"`
	Format      string `json:"format"`
}

// imgInfo probes the REAL format/size of an image file (never the XML's
// claim). Paths come from libvirt's own output, never from the payload.
func (r *Runner) imgInfo(ctx context.Context, path string) (qemuImgInfo, error) {
	bin, ok := r.qemuImgBin()
	if !ok {
		return qemuImgInfo{}, ErrUnavailable
	}
	out, err := runArgv(ctx, bin, "info", "--output=json", path)
	if err != nil {
		return qemuImgInfo{}, fmt.Errorf("qemu-img info başarısız: %w", err)
	}
	var info qemuImgInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return qemuImgInfo{}, fmt.Errorf("qemu-img çıktısı çözülemedi: %w", err)
	}
	return info, nil
}

// imgCheck runs `qemu-img check` (qcow2-only; raw has no checker).
func (r *Runner) imgCheck(ctx context.Context, path string) (ok bool, detail string) {
	bin, okBin := r.qemuImgBin()
	if !okBin {
		return false, "qemu-img yok — bütünlük denetimi yapılamadı"
	}
	c := exec.CommandContext(ctx, bin, "check", path) //nolint:gosec // fixed binary + argv
	var stderr bytes.Buffer
	c.Stderr = &stderr
	err := c.Run()
	if err == nil {
		return true, ""
	}
	d := "qemu-img check başarısız"
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		if len(msg) > 120 {
			msg = msg[:120]
		}
		d = d + ": " + msg
	}
	return false, d
}

// ---------- disk resolution ----------

// diskSource resolves one disk's backing path: volume-backed disks are
// resolved via virsh vol-path, file-backed disks use their path attr.
// The result is libvirt's own view of the world — never payload data.
func (r *Runner) diskSource(ctx context.Context, d xmlDisk) (path, pool, volume string, err error) {
	if d.Source == nil {
		return "", "", "", nil
	}
	if d.Source.Volume != "" {
		pool = d.Source.Pool
		volume = d.Source.Volume
		if pool == "" {
			return "", pool, volume, fmt.Errorf("volume kaynaklı diskte pool yok")
		}
		out, perr := r.virsh(ctx, "vol-path", "--pool", pool, volume)
		if perr != nil {
			return "", pool, volume, fmt.Errorf("vol-path başarısız: %w", perr)
		}
		return strings.TrimSpace(string(out)), pool, volume, nil
	}
	return d.Source.File, "", "", nil
}

// owningPool finds the pool whose target path contains the source path
// (longest-prefix match over pool targets).
func (r *Runner) owningPool(ctx context.Context, pools []PoolInfo, source string) (PoolInfo, bool) {
	var best PoolInfo
	found := false
	for _, p := range pools {
		if p.TargetPath == "" || !p.Active {
			continue
		}
		prefix := strings.TrimSuffix(p.TargetPath, "/") + "/"
		if strings.HasPrefix(source, prefix) {
			if !found || len(p.TargetPath) > len(best.TargetPath) {
				best, found = p, true
			}
		}
	}
	return best, found
}

// buildDiskInfo assembles one DiskInfo with VERIFIED format facts.
func (r *Runner) buildDiskInfo(ctx context.Context, d xmlDisk, pools []PoolInfo) DiskInfo {
	info := DiskInfo{}
	if d.Target != nil {
		info.TargetDev = d.Target.Dev
	}
	if d.Driver != nil {
		info.XMLFormat = d.Driver.Type
	}
	path, pool, volume, rerr := r.diskSource(ctx, d)
	if rerr == nil {
		info.SourcePath = path
		info.Pool = pool
		info.Volume = volume
		if volume != "" {
			info.Managed = ManagedVolume(volume)
		} else if path != "" {
			info.Managed = ManagedVolume(filepath.Base(path))
		}
	}
	if path != "" {
		if qi, ierr := r.imgInfo(ctx, path); ierr == nil {
			info.ActualFormat = qi.Format
			info.VirtualBytes = qi.VirtualSize
			info.ActualBytes = qi.ActualSize
			info.FormatVerified = qi.Format != "" && qi.Format == info.XMLFormat
			if qi.Format == "qcow2" {
				info.CheckOK, info.CheckDetail = r.imgCheck(ctx, path)
			} else {
				info.CheckOK = false
				info.CheckDetail = "check uygulanmadı (yalnız qcow2 denetlenebilir)"
			}
		}
	}
	return info
}

// diskInfos parses all real disks (cdrom/floppy excluded).
func (r *Runner) diskInfos(ctx context.Context, d *xmlDomain, pools []PoolInfo) []DiskInfo {
	var out []DiskInfo
	for _, disk := range d.Devices.Disks {
		if disk.Device != "" && disk.Device != "disk" {
			continue
		}
		out = append(out, r.buildDiskInfo(ctx, disk, pools))
	}
	return out
}

// verifyQcow2Gate enforces criterion 4: EVERY real disk must detect as
// qcow2 AND match its XML claim; otherwise a typed refusal is returned
// before any snapshot mutation runs.
func (r *Runner) verifyQcow2Gate(ctx context.Context, d *xmlDomain, pools []PoolInfo) ([]string, error) {
	var verified []string
	for _, di := range r.diskInfos(ctx, d, pools) {
		if di.SourcePath == "" {
			continue // empty cdrom etc. — filtered above anyway
		}
		if di.ActualFormat != "qcow2" {
			return nil, ErrDiskFormat
		}
		if di.XMLFormat != "" && di.XMLFormat != di.ActualFormat {
			return nil, ErrFormatMismatch
		}
		verified = append(verified, di.TargetDev+"="+di.ActualFormat)
	}
	return verified, nil
}

// ---------- typed operations ----------

// Preflight reports host virtualization capability (criterion 1) —
// report-only: nothing is installed, started or enabled.
func (r *Runner) Preflight(ctx context.Context) (PreflightResult, error) {
	res := PreflightResult{URI: r.uri()}
	model, flags, virt := readCPUInfo(r.cpuInfo())
	res.CPUModel = model
	res.CPUFlags = flags
	res.VirtFlag = virt
	res.MemTotalMiB, res.MemAvailMiB = readMemInfo(r.memInfo())
	res.KVMAvailable, _ = r.kvmAvailable()

	virshFound := false
	if _, ok := resolve(r.Virsh, "virsh"); ok {
		virshFound = true
	}
	_, qemuImgFound := r.qemuImgBin()
	res.QemuImg = qemuImgFound

	if !virshFound {
		res.Notes = append(res.Notes, "virsh bulunamadı — libvirt-clients kurulu değil; salt-rapor modu")
		return res, nil
	}
	if out, err := r.virsh(ctx, "version"); err == nil {
		res.LibvirtVer, res.Hypervisor = parseVirshVersion(out)
	}
	if pools, err := r.listPools(ctx); err == nil {
		res.Pools = pools
	}
	if nets, err := r.listNetworks(ctx); err == nil {
		res.Networks = nets
	}
	res.Bridges = hostBridges(r.sysNet())

	if !res.KVMAvailable {
		if virt == "" {
			res.Notes = append(res.Notes, "CPU sanallaştırma bayrağı yok (vmx/svm) — yalnız TCG (yazılımsal) mümkün")
		} else {
			res.Notes = append(res.Notes, "/dev/kvm erişilebilir değil — yalnız TCG (yazılımsal) mümkün")
		}
	}
	if len(res.Pools) == 0 {
		res.Notes = append(res.Notes, "storage pool yok — VM oluşturmak için bir havuz gerekir")
	} else if len(res.Bridges) == 0 && len(res.Networks) == 0 {
		res.Notes = append(res.Notes, "köprü veya libvirt ağı yok — VM ağ bağlantısı kurulamaz")
	}
	if !qemuImgFound {
		res.Notes = append(res.Notes, "qemu-img yok — disk format doğrulaması ve snapshot kapısı çalışmaz")
	}
	return res, nil
}

// parseVirshVersion extracts the library and hypervisor versions.
func parseVirshVersion(out []byte) (libvirt, hypervisor string) {
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "Using library":
			if _, v, ok := strings.Cut(val, "libvirt "); ok {
				libvirt = v
			}
		case "Hypervisor":
			if _, v, ok := strings.Cut(val, "QEMU "); ok {
				hypervisor = "QEMU " + v
			} else {
				hypervisor = val
			}
		}
	}
	return libvirt, hypervisor
}

// Status summarises the libvirt contract (read-only).
func (r *Runner) Status(ctx context.Context) (StatusResult, error) {
	res := StatusResult{URI: r.uri()}
	res.KVMAvailable, _ = r.kvmAvailable()
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return res, ErrUnavailable
	}
	if out, err := r.virsh(ctx, "version"); err == nil {
		res.LibvirtVer, res.Hypervisor = parseVirshVersion(out)
	}
	names, err := r.listNames(ctx)
	if err != nil {
		return res, err
	}
	if len(names) > MaxList {
		names = names[:MaxList]
	}
	res.DomainsTotal = len(names)
	for _, n := range names {
		st, serr := r.domstate(ctx, n)
		if serr == nil && st == "running" {
			res.Running++
		}
	}
	return res, nil
}

// List returns the domain summary (bounded).
func (r *Runner) List(ctx context.Context, limit int) ([]VMRow, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return nil, ErrUnavailable
	}
	names, err := r.listNames(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) > MaxList {
		names = names[:MaxList]
	}
	if limit <= 0 {
		limit = 200
	}
	rows := make([]VMRow, 0, len(names))
	for _, n := range names {
		if len(rows) >= limit {
			break
		}
		st, serr := r.domstate(ctx, n)
		if serr != nil {
			st = "unknown"
		}
		rows = append(rows, VMRow{Name: n, State: st, Running: st == "running"})
	}
	return rows, nil
}

// Info returns the typed domain report (criterion 5: honest guest-agent
// signal; console METADATA only).
func (r *Runner) Info(ctx context.Context, name string) (VMInfo, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return VMInfo{}, ErrUnavailable
	}
	st, err := r.domstate(ctx, name)
	if err != nil {
		return VMInfo{}, err
	}
	d, err := r.dumpDomain(ctx, name)
	if err != nil {
		return VMInfo{}, err
	}
	pools, _ := r.listPools(ctx)
	info := VMInfo{
		Name:    d.Name,
		UUID:    d.UUID,
		State:   st,
		Running: st == "running",
		VCPU:    d.VCPU,
		MemMiB:  d.memToMiB(),
		Arch:    d.OS.Type.Arch,
	}
	info.Disks = r.diskInfos(ctx, d, pools)
	for _, ifc := range d.Devices.Ifaces {
		ni := NicInfo{Model: "virtio"}
		if ifc.MAC != nil {
			ni.MAC = ifc.MAC.Address
		}
		if ifc.Source != nil {
			ni.Bridge = ifc.Source.Bridge
			ni.Network = ifc.Source.Network
		}
		if ifc.Model != nil {
			ni.Model = ifc.Model.Type
		}
		info.Interfaces = append(info.Interfaces, ni)
	}
	if len(d.Devices.Gfx) > 0 {
		g := d.Devices.Gfx[0]
		info.Console.GraphicsType = g.Type
		info.Console.Port = g.Port
		listen := ""
		if g.Listen != nil {
			listen = g.Listen.Address
			if listen == "" {
				listen = strings.TrimSpace(g.Listen.Text)
			}
		}
		info.Console.Listen = listen
		info.Console.LocalOnly = listen == "" || listen == "127.0.0.1" || listen == "::1" || listen == "localhost"
	} else {
		info.Console.LocalOnly = true
	}
	for _, c := range d.Devices.Channels {
		if c.Target != nil && c.Target.Name == "org.qemu.guest_agent.0" {
			info.GuestAgent = true
			break
		}
	}
	for _, c := range d.Devices.Consoles {
		if c.Type == "pty" {
			info.Console.Serial = true
			break
		}
	}
	return info, nil
}

// Create defines a new VM with closed-range resource limits (criterion 2),
// a managed sc- volume in the given pool and a local-only VNC console.
// A failed define rolls the created volume back (best-effort).
func (r *Runner) Create(ctx context.Context, name string, vcpu int, memMiB, diskGiB int64, pool, bridge string) (CreateResult, error) {
	if !ValidVMName(name) {
		return CreateResult{}, ErrInvalidName
	}
	if !ValidVCPU(int64(vcpu)) || !ValidMemMiB(memMiB) || !ValidDiskGiB(diskGiB) {
		return CreateResult{}, ErrInvalidLimits
	}
	if !ValidPoolName(pool) {
		return CreateResult{}, ErrInvalidPool
	}
	if !ValidBridge(bridge) {
		return CreateResult{}, ErrInvalidBridge
	}
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return CreateResult{}, ErrUnavailable
	}
	if r.exists(ctx, name) {
		return CreateResult{}, ErrExists
	}
	pools, err := r.listPools(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	var target *PoolInfo
	for i := range pools {
		if pools[i].Name == pool {
			target = &pools[i]
			break
		}
	}
	if target == nil {
		return CreateResult{}, fmt.Errorf("%w: pool bulunamadı: %s", ErrNotFound, pool)
	}
	if !target.Active {
		return CreateResult{}, fmt.Errorf("%w: pool etkin değil: %s", ErrConflict, pool)
	}
	if target.FreeBytes > 0 && target.FreeBytes < diskGiB<<30 {
		return CreateResult{}, fmt.Errorf("%w: pool boş alanı yetersiz (%s)", ErrInsufficient, pool)
	}
	// Bridge kind: host bridge wins, libvirt network is the fallback;
	// anything else is a typed refusal (never a guessed device).
	bridgeKind := ""
	bridgeSet := map[string]bool{}
	for _, b := range hostBridges(r.sysNet()) {
		bridgeSet[b] = true
	}
	if bridgeSet[bridge] {
		bridgeKind = "bridge"
	} else {
		nets, nerr := r.listNetworks(ctx)
		if nerr != nil {
			return CreateResult{}, nerr
		}
		for _, n := range nets {
			if n == bridge {
				bridgeKind = "network"
				break
			}
		}
	}
	if bridgeKind == "" {
		return CreateResult{}, fmt.Errorf("%w: köprü/ağ bulunamadı: %s", ErrInvalidBridge, bridge)
	}
	totalMiB, _ := readMemInfo(r.memInfo())
	if totalMiB > 0 && totalMiB < memMiB {
		return CreateResult{}, fmt.Errorf("%w: host toplam belleği %d MiB", ErrInsufficient, totalMiB)
	}
	kvm, _ := r.kvmAvailable()
	accelerator := "qemu"
	if kvm {
		accelerator = "kvm"
	}

	volume := VolumePrefix + name + "-root"
	if _, err := r.virsh(ctx, "vol-create-as", "--pool", pool, "--name", volume,
		"--capacity", strconv.FormatInt(diskGiB, 10)+"G", "--format", "qcow2"); err != nil {
		return CreateResult{}, fmt.Errorf("birim oluşturulamadı: %w", err)
	}
	uuid, err := newUUID()
	if err != nil {
		return CreateResult{}, err
	}
	xmlContent := r.renderDomain(name, uuid, vcpu, memMiB, pool, volume, bridge, bridgeKind, accelerator)
	staged, err := r.stageXML(name, xmlContent)
	if err != nil {
		return CreateResult{}, err
	}
	defer os.Remove(staged)
	if _, err := r.virsh(ctx, "define", staged); err != nil {
		// ROLLBACK: the volume was created but the domain was not defined —
		// remove the orphan volume (best-effort) and report honestly.
		_, _ = r.virsh(ctx, "vol-delete", "--pool", pool, volume)
		return CreateResult{}, fmt.Errorf("%w: define başarısız, birim geri alındı", ErrConflict)
	}
	st, _ := r.domstate(ctx, name)
	return CreateResult{
		Name: name, UUID: uuid, VCPU: vcpu, MemMiB: memMiB, DiskGiB: diskGiB,
		Pool: pool, Volume: volume, Bridge: bridge, BridgeKind: bridgeKind,
		Accelerator: accelerator, State: st,
	}, nil
}

// Clone copies a STOPPED source VM: every disk is converted into a fresh
// managed qcow2 volume in the source's anchor pool and a new domain is
// defined with a fresh UUID/MAC. Running sources are a typed refusal.
func (r *Runner) Clone(ctx context.Context, src, name string) (CloneResult, error) {
	if !ValidVMName(name) {
		return CloneResult{}, ErrInvalidName
	}
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return CloneResult{}, ErrUnavailable
	}
	if !r.exists(ctx, src) {
		return CloneResult{}, ErrNotFound
	}
	st, err := r.domstate(ctx, src)
	if err != nil {
		return CloneResult{}, err
	}
	if st != "shut off" {
		return CloneResult{}, fmt.Errorf("%w: klon için kaynak kapalı olmalı (durum: %s)", ErrConflict, st)
	}
	if r.exists(ctx, name) {
		return CloneResult{}, ErrExists
	}
	d, err := r.dumpDomain(ctx, src)
	if err != nil {
		return CloneResult{}, err
	}
	pools, perr := r.listPools(ctx)
	if perr != nil {
		return CloneResult{}, perr
	}
	// Anchor pool = the pool of the source's first volume-backed disk.
	anchor := ""
	for _, disk := range d.Devices.Disks {
		if disk.Device != "" && disk.Device != "disk" {
			continue
		}
		if _, pool, _, rerr := r.diskSource(ctx, disk); rerr == nil && pool != "" {
			anchor = pool
			break
		}
		if pi, ok := r.owningPool(ctx, pools, func() string {
			p, _, _, e := r.diskSource(ctx, disk)
			if e != nil {
				return ""
			}
			return p
		}()); ok {
			anchor = pi.Name
			break
		}
	}
	if anchor == "" {
		return CloneResult{}, fmt.Errorf("%w: kaynak diskleri bir storage pool içinde değil", ErrForeignVolume)
	}
	// qemu-img convert writes a FILE into the pool target path — only dir
	// pools have one; anything else is an honest typed refusal (no guessing
	// where the copy should live).
	for _, p := range pools {
		if p.Name == anchor {
			if p.Type != "dir" {
				return CloneResult{}, fmt.Errorf("%w: klon yalnız dir tipi havuzda desteklenir (%s: %s)", ErrConflict, anchor, p.Type)
			}
			break
		}
	}
	uuid, err := newUUID()
	if err != nil {
		return CloneResult{}, err
	}
	mac, err := newMAC()
	if err != nil {
		return CloneResult{}, err
	}
	// Copy every real disk (converted to managed qcow2 volumes).
	type copyJob struct{ srcPath, volume string }
	var jobs []copyJob
	var created []string
	cleanup := func() {
		for _, v := range created {
			_, _ = r.virsh(ctx, "vol-delete", "--pool", anchor, v)
		}
	}
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	for _, disk := range d.Devices.Disks {
		if disk.Device != "" && disk.Device != "disk" {
			continue
		}
		srcPath, _, _, rerr := r.diskSource(ctx, disk)
		if rerr != nil || srcPath == "" {
			continue
		}
		// Dev letter follows the COPY index (not the raw disk range) so the
		// volume name suffix always matches the rendered target dev.
		dev := "vd" + string(rune('a'+len(jobs)%26))
		volume := VolumePrefix + name + "-" + dev
		bin, okBin := r.qemuImgBin()
		if !okBin {
			return CloneResult{}, ErrUnavailable
		}
		if _, err := runArgv(ctx, bin, "convert", "-O", "qcow2", srcPath,
			filepath.Join(strings.TrimSuffix(func() string {
				for _, p := range pools {
					if p.Name == anchor {
						return p.TargetPath
					}
				}
				return ""
			}(), "/"), volume)); err != nil {
			return CloneResult{}, fmt.Errorf("disk kopyası başarısız (%s): %w", dev, err)
		}
		created = append(created, volume)
		jobs = append(jobs, copyJob{volume: volume})
	}
	// The converted files were written by qemu-img DIRECTLY into the pool
	// target — libvirt does not know them until a pool refresh (real CI
	// finding CI-47: deleting the clone later resolved vol-path against a
	// pool that had never indexed the volume). Refresh is REQUIRED, not
	// best-effort: an unindexed volume would be undeletable.
	if _, err := r.virsh(ctx, "pool-refresh", "--pool", anchor); err != nil {
		return CloneResult{}, fmt.Errorf("%w: havuz tazelenemedi — kopya birimleri libvirt'e kaydedilemedi", ErrConflict)
	}
	if len(jobs) == 0 {
		return CloneResult{}, fmt.Errorf("%w: kopyalanabilir disk yok", ErrConflict)
	}
	// Rebuild a minimal managed domain from the parsed source.
	memMiB := d.memToMiB()
	if memMiB < MinMemMiB {
		memMiB = MinMemMiB
	}
	vcpu := d.VCPU
	if vcpu < MinVCPU {
		vcpu = MinVCPU
	}
	bridge, bridgeKind := "", "network"
	nets, _ := r.listNetworks(ctx)
	for _, ifc := range d.Devices.Ifaces {
		if ifc.Source == nil {
			continue
		}
		if ifc.Source.Bridge != "" {
			bridge, bridgeKind = ifc.Source.Bridge, "bridge"
		} else if ifc.Source.Network != "" {
			bridge, bridgeKind = ifc.Source.Network, "network"
		}
		break
	}
	if bridge == "" && len(nets) > 0 {
		bridge, bridgeKind = nets[0], "network"
	}
	if bridge == "" {
		cleanup()
		cleanup = nil
		return CloneResult{}, ErrInvalidBridge
	}
	accelerator := "qemu"
	if kvm, _ := r.kvmAvailable(); kvm {
		accelerator = "kvm"
	}
	var b strings.Builder
	domainType := "qemu"
	if accelerator == "kvm" {
		domainType = "kvm"
	}
	fmt.Fprintf(&b, "<domain type='%s'>\n", domainType)
	fmt.Fprintf(&b, "  <name>%s</name>\n", esc(name))
	fmt.Fprintf(&b, "  <uuid>%s</uuid>\n", esc(uuid))
	fmt.Fprintf(&b, "  <memory unit='MiB'>%d</memory>\n", memMiB)
	fmt.Fprintf(&b, "  <vcpu>%d</vcpu>\n", vcpu)
	fmt.Fprintf(&b, "  <os><type arch='x86_64' machine='pc'>hvm</type></os>\n")
	fmt.Fprintf(&b, "  <features><acpi/><apic/></features>\n")
	fmt.Fprintf(&b, "  <clock offset='utc'/>\n  <devices>\n")
	for i, j := range jobs {
		dev := "vd" + string(rune('a'+i%26))
		fmt.Fprintf(&b, "    <disk type='volume' device='disk'><driver name='qemu' type='qcow2'/>"+
			"<source pool='%s' volume='%s'/><target dev='%s' bus='virtio'/></disk>\n",
			esc(anchor), esc(j.volume), dev)
	}
	fmt.Fprintf(&b, "    <interface type='%s'>\n", bridgeKind)
	if bridgeKind == "network" {
		fmt.Fprintf(&b, "      <source network='%s'/>\n", esc(bridge))
	} else {
		fmt.Fprintf(&b, "      <source bridge='%s'/>\n", esc(bridge))
	}
	fmt.Fprintf(&b, "      <model type='virtio'/><mac address='%s'/>\n    </interface>\n", esc(mac))
	fmt.Fprintf(&b, "    <console type='pty'><target type='serial' port='0'/></console>\n")
	fmt.Fprintf(&b, "    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>\n")
	fmt.Fprintf(&b, "    <channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>\n")
	fmt.Fprintf(&b, "    <memballoon model='virtio'/>\n  </devices>\n</domain>\n")

	staged, err := r.stageXML(name, b.String())
	if err != nil {
		return CloneResult{}, err
	}
	defer os.Remove(staged)
	if _, err := r.virsh(ctx, "define", staged); err != nil {
		return CloneResult{}, fmt.Errorf("%w: klon define başarısız, birimler geri alındı", ErrConflict)
	}
	cleanup = nil
	return CloneResult{Source: src, Name: name, UUID: uuid, Volume: jobs[0].volume}, nil
}

// Start converges to the desired state "running"; an already-running VM
// is an honest no-op (Already=true).
func (r *Runner) Start(ctx context.Context, name string) (MutateResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return MutateResult{}, ErrUnavailable
	}
	st, err := r.domstate(ctx, name)
	if err != nil {
		return MutateResult{}, err
	}
	if st == "running" {
		return MutateResult{Name: name, Action: "start", Already: true, State: st}, nil
	}
	if _, err := r.virsh(ctx, "start", name); err != nil {
		return MutateResult{}, fmt.Errorf("start başarısız: %w", err)
	}
	st, _ = r.domstate(ctx, name)
	return MutateResult{Name: name, Action: "start", State: st}, nil
}

// Stop converges to "shut off". Graceful shutdown waits the bounded
// window and NEVER escalates on its own; force=true executes a destroy
// (power cut) as the explicitly requested action.
func (r *Runner) Stop(ctx context.Context, name string, force bool) (StopResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return StopResult{}, ErrUnavailable
	}
	st, err := r.domstate(ctx, name)
	if err != nil {
		return StopResult{}, err
	}
	if st == "shut off" {
		return StopResult{Name: name, Already: true, State: st}, nil
	}
	if force {
		if _, err := r.virsh(ctx, "destroy", name); err != nil {
			return StopResult{}, fmt.Errorf("destroy başarısız: %w", err)
		}
		st, _ = r.domstate(ctx, name)
		return StopResult{Name: name, Forced: true, State: st}, nil
	}
	if _, err := r.virsh(ctx, "shutdown", name); err != nil {
		return StopResult{}, fmt.Errorf("shutdown başarısız: %w", err)
	}
	deadline := time.Now().Add(r.stopGrace())
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		cur, serr := r.domstate(ctx, name)
		if serr == nil && cur == "shut off" {
			return StopResult{Name: name, State: cur}, nil
		}
	}
	cur, _ := r.domstate(ctx, name)
	return StopResult{Name: name, GracefulTimedOut: true, State: cur},
		fmt.Errorf("%w: graceful shutdown %s içinde tamamlanmadı (durum: %s) — force seçeneğini kullanın ya da guest içinden kapatın",
			ErrConflict, r.stopGrace(), cur)
}

// Reboot is a graceful restart of a RUNNING domain (typed refusal
// otherwise — libvirt would silently ignore it on a stopped VM).
func (r *Runner) Reboot(ctx context.Context, name string) (MutateResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return MutateResult{}, ErrUnavailable
	}
	st, err := r.domstate(ctx, name)
	if err != nil {
		return MutateResult{}, err
	}
	if st != "running" {
		return MutateResult{}, fmt.Errorf("%w: reboot yalnız çalışan VM'de (durum: %s)", ErrConflict, st)
	}
	if _, err := r.virsh(ctx, "reboot", name); err != nil {
		return MutateResult{}, fmt.Errorf("reboot başarısız: %w", err)
	}
	return MutateResult{Name: name, Action: "reboot", State: st}, nil
}

// Delete removes a STOPPED domain and ONLY its managed sc- volumes
// (criterion 3). A foreign disk refuses the WHOLE delete — the VM stays
// defined and nothing is touched. A second run after a partial volume
// failure recovers (undefine treats not-found as done).
func (r *Runner) Delete(ctx context.Context, name string) (DeleteResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return DeleteResult{}, ErrUnavailable
	}
	st, err := r.domstate(ctx, name)
	if err != nil {
		return DeleteResult{}, err
	}
	if st != "shut off" {
		return DeleteResult{}, fmt.Errorf("%w: silme için VM kapalı olmalı (durum: %s)", ErrConflict, st)
	}
	d, err := r.dumpDomain(ctx, name)
	if err != nil {
		return DeleteResult{}, err
	}
	pools, perr := r.listPools(ctx)
	if perr != nil {
		return DeleteResult{}, perr
	}
	type volRef struct{ pool, name string }
	var vols []volRef
	for _, disk := range d.Devices.Disks {
		if disk.Device != "" && disk.Device != "disk" {
			continue
		}
		src, pool, volume, rerr := r.diskSource(ctx, disk)
		if rerr != nil {
			return DeleteResult{}, fmt.Errorf("%w: disk kaynağı çözülemedi", ErrForeignVolume)
		}
		if volume == "" {
			vol, ok := r.owningPool(ctx, pools, src)
			if !ok {
				return DeleteResult{}, fmt.Errorf("%w: disk bir havuz içinde değil (%s)", ErrForeignVolume, src)
			}
			volume = filepath.Base(src)
			pool = vol.Name
		}
		vols = append(vols, volRef{pool: pool, name: volume})
		// Managed-namespace gate (criterion 3): refuse foreign volumes.
		if !ManagedVolume(vols[len(vols)-1].name) {
			return DeleteResult{}, fmt.Errorf("%w: %s", ErrForeignVolume, vols[len(vols)-1].name)
		}
	}
	// Internal snapshots BLOCK undefine (libvirt: "cannot delete inactive
	// domain with N snapshots"). The operator already chose delete — remove
	// them first, honestly typed if one refuses (state stays recoverable).
	if names, lerr := r.snapshotNames(ctx, name); lerr == nil && len(names) > 0 {
		for _, sn := range names {
			if _, serr := r.virsh(ctx, "snapshot-delete", "--domain", name, "--snapshotname", sn); serr != nil {
				return DeleteResult{}, fmt.Errorf("%w: snapshot silinemedi (%s)", ErrConflict, sn)
			}
		}
	}
	// Undefine first (config), then volumes (data) — a repeated run
	// recovers from a partial previous delete.
	if d.OS.NVRAM != nil {
		_, err = r.virsh(ctx, "undefine", "--nvram", name)
	} else {
		_, err = r.virsh(ctx, "undefine", name)
	}
	if err != nil {
		if r.exists(ctx, name) {
			return DeleteResult{}, fmt.Errorf("undefine başarısız: %w", err)
		}
		// Already undefined by a previous partial run — continue volumes.
	}
	res := DeleteResult{Name: name, Undefined: true}
	for _, v := range vols {
		if v.pool == "" {
			return res, fmt.Errorf("%w: birimin havuzu çözülemedi (%s)", ErrConflict, v.name)
		}
		if _, err := r.virsh(ctx, "vol-delete", "--pool", v.pool, v.name); err != nil {
			return res, fmt.Errorf("%w: birim silinemedi (%s/%s)", ErrConflict, v.pool, v.name)
		}
		res.VolumesRemoved = append(res.VolumesRemoved, v.pool+"/"+v.name)
	}
	return res, nil
}

// DiskList reports every disk with VERIFIED format facts (criterion 3/4).
func (r *Runner) DiskList(ctx context.Context, name string) ([]DiskInfo, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return nil, ErrUnavailable
	}
	if _, err := r.domstate(ctx, name); err != nil {
		return nil, err
	}
	d, err := r.dumpDomain(ctx, name)
	if err != nil {
		return nil, err
	}
	pools, _ := r.listPools(ctx)
	return r.diskInfos(ctx, d, pools), nil
}

// DiskAdd attaches a NEW managed qcow2 volume from the pool (persistent,
// virtio) with the next free vd* target. A failed attach rolls the
// volume back (best-effort).
func (r *Runner) DiskAdd(ctx context.Context, name, pool string, sizeGiB int64) (DiskAddResult, error) {
	if !ValidPoolName(pool) || !ValidDiskGiB(sizeGiB) {
		return DiskAddResult{}, ErrInvalidLimits
	}
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return DiskAddResult{}, ErrUnavailable
	}
	if _, err := r.domstate(ctx, name); err != nil {
		return DiskAddResult{}, err
	}
	pools, err := r.listPools(ctx)
	if err != nil {
		return DiskAddResult{}, err
	}
	var target *PoolInfo
	for i := range pools {
		if pools[i].Name == pool {
			target = &pools[i]
			break
		}
	}
	if target == nil {
		return DiskAddResult{}, fmt.Errorf("%w: pool bulunamadı: %s", ErrNotFound, pool)
	}
	if !target.Active {
		return DiskAddResult{}, fmt.Errorf("%w: pool etkin değil: %s", ErrConflict, pool)
	}
	if target.FreeBytes > 0 && target.FreeBytes < sizeGiB<<30 {
		return DiskAddResult{}, fmt.Errorf("%w: pool boş alanı yetersiz (%s)", ErrInsufficient, pool)
	}
	d, err := r.dumpDomain(ctx, name)
	if err != nil {
		return DiskAddResult{}, err
	}
	used := map[string]bool{}
	for _, disk := range d.Devices.Disks {
		if disk.Target != nil {
			used[disk.Target.Dev] = true
		}
	}
	dev := ""
	for c := 'a'; c <= 'z'; c++ {
		cand := "vd" + string(c)
		if !used[cand] {
			dev = cand
			break
		}
	}
	if dev == "" {
		return DiskAddResult{}, ErrLimit
	}
	volume := VolumePrefix + name + "-" + dev
	out, lerr := r.virsh(ctx, "vol-list", "--pool", pool, "--name")
	if lerr == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == volume {
				return DiskAddResult{}, ErrExists
			}
		}
	}
	if _, err := r.virsh(ctx, "vol-create-as", "--pool", pool, "--name", volume,
		"--capacity", strconv.FormatInt(sizeGiB, 10)+"G", "--format", "qcow2"); err != nil {
		return DiskAddResult{}, fmt.Errorf("birim oluşturulamadı: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<disk type='volume' device='disk'><driver name='qemu' type='qcow2'/>"+
		"<source pool='%s' volume='%s'/><target dev='%s' bus='virtio'/></disk>\n",
		esc(pool), esc(volume), dev)
	staged, err := r.stageXML(name, b.String())
	if err != nil {
		return DiskAddResult{}, err
	}
	defer os.Remove(staged)
	if _, err := r.virsh(ctx, "attach-device", name, staged, "--persistent"); err != nil {
		_, _ = r.virsh(ctx, "vol-delete", "--pool", pool, volume)
		return DiskAddResult{}, fmt.Errorf("%w: attach başarısız, birim geri alındı", ErrConflict)
	}
	return DiskAddResult{Name: name, Pool: pool, Volume: volume, TargetDev: dev, SizeGiB: sizeGiB}, nil
}

// snapshotNames lists snapshot names of a domain.
func (r *Runner) snapshotNames(ctx context.Context, domain string) ([]string, error) {
	out, err := r.virsh(ctx, "snapshot-list", "--domain", domain, "--name")
	if err != nil {
		return nil, fmt.Errorf("snapshot listesi alınamadı: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// SnapshotList returns snapshot summaries (current marker included).
func (r *Runner) SnapshotList(ctx context.Context, name string) ([]SnapshotRow, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return nil, ErrUnavailable
	}
	if _, err := r.domstate(ctx, name); err != nil {
		return nil, err
	}
	names, err := r.snapshotNames(ctx, name)
	if err != nil {
		return nil, err
	}
	current := ""
	if out, cerr := r.virsh(ctx, "snapshot-current", "--domain", name, "--name"); cerr == nil {
		current = strings.TrimSpace(string(out))
	}
	rows := make([]SnapshotRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, SnapshotRow{Name: n, Current: n == current})
	}
	return rows, nil
}

// SnapshotCreate creates an INTERNAL libvirt snapshot after verifying
// every real disk is qcow2 and matches its XML claim (criterion 4).
func (r *Runner) SnapshotCreate(ctx context.Context, domain, snap, description string) (SnapshotCreateResult, error) {
	if !ValidVMName(domain) || !ValidVMName(snap) {
		return SnapshotCreateResult{}, ErrInvalidName
	}
	if !ValidDescription(description) {
		return SnapshotCreateResult{}, ErrInvalidLimits
	}
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return SnapshotCreateResult{}, ErrUnavailable
	}
	if _, err := r.domstate(ctx, domain); err != nil {
		return SnapshotCreateResult{}, err
	}
	d, err := r.dumpDomain(ctx, domain)
	if err != nil {
		return SnapshotCreateResult{}, err
	}
	pools, _ := r.listPools(ctx)
	verified, err := r.verifyQcow2Gate(ctx, d, pools)
	if err != nil {
		return SnapshotCreateResult{}, err
	}
	names, err := r.snapshotNames(ctx, domain)
	if err != nil {
		return SnapshotCreateResult{}, err
	}
	for _, n := range names {
		if n == snap {
			return SnapshotCreateResult{}, ErrExists
		}
	}
	if _, err := r.virsh(ctx, "snapshot-create-as", "--domain", domain,
		"--name", snap, "--description", description); err != nil {
		return SnapshotCreateResult{}, fmt.Errorf("snapshot oluşturulamadı: %w", err)
	}
	// Verify presence (honest success — the snapshot is really listed).
	found := false
	if names, _ := r.snapshotNames(ctx, domain); names != nil {
		for _, n := range names {
			if n == snap {
				found = true
				break
			}
		}
	}
	if !found {
		return SnapshotCreateResult{}, fmt.Errorf("%w: snapshot oluşturuldu ama listede doğrulanamadı", ErrConflict)
	}
	return SnapshotCreateResult{Name: snap, Domain: domain, VerifiedFormats: verified}, nil
}

// SnapshotRevert rolls the domain back to a snapshot (qcow2 gate
// re-verified — criterion 4). The operation is NOT retry-safe: reverting
// twice discards the intermediate state twice.
func (r *Runner) SnapshotRevert(ctx context.Context, domain, snap string) (MutateResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return MutateResult{}, ErrUnavailable
	}
	if _, err := r.domstate(ctx, domain); err != nil {
		return MutateResult{}, err
	}
	names, err := r.snapshotNames(ctx, domain)
	if err != nil {
		return MutateResult{}, err
	}
	found := false
	for _, n := range names {
		if n == snap {
			found = true
			break
		}
	}
	if !found {
		return MutateResult{}, ErrNotFound
	}
	d, err := r.dumpDomain(ctx, domain)
	if err != nil {
		return MutateResult{}, err
	}
	pools, _ := r.listPools(ctx)
	if _, err := r.verifyQcow2Gate(ctx, d, pools); err != nil {
		return MutateResult{}, err
	}
	if _, err := r.virsh(ctx, "snapshot-revert", "--domain", domain, "--snapshotname", snap); err != nil {
		return MutateResult{}, fmt.Errorf("snapshot-revert başarısız: %w", err)
	}
	return MutateResult{Name: domain, Action: "snapshot-revert", State: snap}, nil
}

// SnapshotDelete removes one snapshot (typed refusal when missing).
func (r *Runner) SnapshotDelete(ctx context.Context, domain, snap string) (MutateResult, error) {
	if _, ok := resolve(r.Virsh, "virsh"); !ok {
		return MutateResult{}, ErrUnavailable
	}
	if _, err := r.domstate(ctx, domain); err != nil {
		return MutateResult{}, err
	}
	names, err := r.snapshotNames(ctx, domain)
	if err != nil {
		return MutateResult{}, err
	}
	found := false
	for _, n := range names {
		if n == snap {
			found = true
			break
		}
	}
	if !found {
		return MutateResult{}, ErrNotFound
	}
	if _, err := r.virsh(ctx, "snapshot-delete", "--domain", domain, "--snapshotname", snap); err != nil {
		return MutateResult{}, fmt.Errorf("snapshot-delete başarısız: %w", err)
	}
	return MutateResult{Name: domain, Action: "snapshot-delete", State: snap}, nil
}
