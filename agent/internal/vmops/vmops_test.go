//go:build linux

// Hermetic unit tests for the Faz 12 VM adapter. ALL external programs are
// fake shell shims in a temp directory: the fake virsh keeps a small STATE
// directory (domains/pools/volumes/snapshots as real files) so multi-step
// lifecycles — create → start → snapshot → delete, plus rollback paths —
// behave like the real tool without libvirt installed (ADR-0016 test
// discipline, same approach as mailops).
package vmops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureDomainXML = `<domain type='kvm'>
  <name>vm1</name><uuid>11111111-2222-3333-4444-555555555555</uuid>
  <memory unit='KiB'>524288</memory><vcpu>2</vcpu>
  <os><type arch='x86_64' machine='pc'>hvm</type></os>
  <devices>
    <disk type='volume' device='disk'><driver name='qemu' type='qcow2'/><source pool='default' volume='sc-vm1-root'/><target dev='vda' bus='virtio'/></disk>
    <interface type='bridge'><mac address='52:54:00:aa:bb:01'/><source bridge='br0'/><model type='virtio'/></interface>
    <console type='pty'><target type='serial' port='0'/></console>
    <graphics type='vnc' port='5900' autoport='yes' listen='127.0.0.1'/>
    <channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel>
    <memballoon model='virtio'/>
  </devices>
</domain>
`

// shims builds a fake tool directory: a stateful virsh and qemu-img that
// keep domains/pools/volumes/snapshots as real files under a state dir.
func shims(t *testing.T) (r *Runner, state string, logPath string) {
	t.Helper()
	bin := t.TempDir()
	state = t.TempDir()
	logPath = filepath.Join(t.TempDir(), "calls.log")
	for _, d := range []string{"domains", "state", "pools", "pools_active", "vols", "snapshots", "fmt"} {
		if err := os.MkdirAll(filepath.Join(state, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	virsh := `#!/bin/sh
echo "virsh $@" >> "$LOG"
[ "$1" = "-q" ] && shift
[ "$1" = "--connect" ] && shift 2
CMD="$1"; shift
D="$VMSHIM/domains"; S="$VMSHIM/state"; P="$VMSHIM/pools"; PA="$VMSHIM/pools_active"
V="$VMSHIM/vols"; N="$VMSHIM/snapshots"
case "$CMD" in
  domuuid) [ -f "$D/$1.xml" ] && exit 0; exit 1 ;;
  domstate) [ -f "$S/$1" ] && cat "$S/$1" || echo "shut off" ;;
  list) for f in "$D"/*.xml; do [ -e "$f" ] && basename "$f" .xml; done ;;
  pool-list)
    if [ "$1" = "--all" ]; then
      for f in "$P"/*.xml; do [ -e "$f" ] && basename "$f" .xml; done
    else
      ls "$PA" 2>/dev/null
    fi ;;
  pool-dumpxml) cat "$P/$1.xml" 2>/dev/null || exit 1 ;;
  pool-refresh) exit 0 ;;
  net-list) cat "$VMSHIM/networks" 2>/dev/null ;;
  version) echo "Using library: libvirt 10.10.0"; echo "Using API: QEMU 10.10.0"; echo "Running hypervisor: QEMU 8.2.0"; echo "Hypervisor: QEMU 8.2.0" ;;
  dumpxml) [ -f "$D/$1.xml" ] && cat "$D/$1.xml" || exit 1 ;;
  vol-path) pool="$2"; vol="$3"; echo "$V/$pool/$vol" ;;
  vol-create-as) pool="$2"; name="$4"
    [ -e "$V/$pool/$name" ] && exit 1
    mkdir -p "$V/$pool"; : > "$V/$pool/$name" ;;
  vol-delete) pool="$2"; name="$3"; rm -f "$V/$pool/$name" ;;
  vol-list) ls "$V/$2" 2>/dev/null ;;
  define) [ -n "$FAIL_DEFINE" ] && exit 1
    n=$(sed -n 's:.*<name>\(.*\)</name>.*:\1:p' "$1" | head -1)
    mkdir -p "$D"; cp "$1" "$D/$n.xml" ;;
  attach-device) [ -n "$FAIL_ATTACH" ] && exit 1; exit 0 ;;
  start) echo running > "$S/$1" ;;
  shutdown) [ -n "$CONVERGE" ] && echo "shut off" > "$S/$1"; exit 0 ;;
  destroy) echo "shut off" > "$S/$1" ;;
  reboot) exit 0 ;;
  undefine)
    if [ -n "$FAIL_UNDEFINE" ]; then
      [ -n "$UNDEF_GONE" ] && rm -f "$D/$1.xml" "$S/$1"
      exit 1
    fi
    rm -f "$D/$1.xml" "$S/$1" ;;
  snapshot-list) dom="$2"; for f in "$N/$dom"/*; do [ -e "$f" ] && basename "$f"; done ;;
  snapshot-current) cat "$VMSHIM/current" 2>/dev/null ;;
  snapshot-create-as) dom="$2"; name="$4"
    [ -e "$N/$dom/$name" ] && exit 1
    [ -n "$FAIL_SNAP" ] && exit 1
    mkdir -p "$N/$dom"; : > "$N/$dom/$name" ;;
  snapshot-revert) exit 0 ;;
  snapshot-delete) dom="$2"; name="$4"; rm -f "$N/$dom/$name" ;;
  *) echo "unknown cmd: $CMD" >&2; exit 1 ;;
esac
exit 0`
	qemuimg := `#!/bin/sh
echo "qemu-img $@" >> "$LOG"
if [ "$1" = "info" ]; then
  last=""
  for a in "$@"; do last="$a"; done
  fmt="${SC_FMT_DEFAULT:-qcow2}"
  base=$(basename "$last")
  [ -f "$VMSHIM/fmt/$base" ] && fmt=$(cat "$VMSHIM/fmt/$base")
  printf '{"virtual-size":10737418240,"actual-size":2097152,"format":"%s"}\n' "$fmt"
  exit 0
fi
if [ "$1" = "check" ]; then [ -n "$FAIL_CHECK" ] && exit 1; exit 0; fi
if [ "$1" = "convert" ]; then [ -n "$FAIL_CONVERT" ] && exit 1; exit 0; fi
exit 0`

	virshPath := filepath.Join(bin, "virsh")
	qemuPath := filepath.Join(bin, "qemu-img")
	if err := os.WriteFile(virshPath, []byte(virsh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qemuPath, []byte(qemuimg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOG", logPath)
	t.Setenv("VMSHIM", state)
	for _, kv := range []string{"FAIL_DEFINE", "FAIL_ATTACH", "FAIL_UNDEFINE", "UNDEF_GONE",
		"FAIL_SNAP", "FAIL_CHECK", "FAIL_CONVERT", "CONVERGE", "SC_FMT_DEFAULT"} {
		t.Setenv(kv, "")
	}
	r = &Runner{
		Virsh: virshPath, QemuImg: qemuPath,
		URI:         "qemu:///test",
		CPUInfoPath: filepath.Join(state, "cpuinfo"),
		MemInfoPath: filepath.Join(state, "meminfo"),
		KVMPath:     filepath.Join(state, "kvm-device"),
		SysNetPath:  filepath.Join(state, "sysnet"),
		BaseDir:     filepath.Join(state, "staging"),
		StopGrace:   700 * time.Millisecond,
	}
	return r, state, logPath
}

// seedHost writes cpuinfo/meminfo fixtures and a /dev/kvm stand-in.
func seedHost(t *testing.T, r *Runner, kvm bool) {
	t.Helper()
	cpu := "processor : 0\nmodel name : Test CPU @ 2GHz\nflags : fpu sse " + map[bool]string{true: "vmx", false: "hypervisor"}[kvm] + "\n"
	if err := os.WriteFile(r.CPUInfoPath, []byte(cpu), 0o644); err != nil {
		t.Fatal(err)
	}
	mem := "MemTotal:        1048576 kB\nMemAvailable:     524288 kB\n"
	if err := os.WriteFile(r.MemInfoPath, []byte(mem), 0o644); err != nil {
		t.Fatal(err)
	}
	if kvm {
		if err := os.WriteFile(r.KVMPath, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// seedPool writes a dir pool XML (100 GiB capacity, ~99 GiB free).
func seedPool(t *testing.T, state, name string) {
	t.Helper()
	pool := "<pool type='dir'><name>" + name + "</name>" +
		"<capacity unit='bytes'>107374182400</capacity>" +
		"<allocation unit='bytes'>1073741824</allocation>" +
		"<available unit='bytes'>106300440576</available>" +
		"<target><path>" + filepath.Join(state, "pool-"+name) + "</path></target></pool>"
	if err := os.WriteFile(filepath.Join(state, "pools", name+".xml"), []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "pools_active", name), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedBridge creates a bridge entry under the fake /sys/class/net.
func seedBridge(t *testing.T, r *Runner, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(r.SysNetPath, name, "bridge"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// seedPoolType writes a pool XML with an explicit type (dir/logical/...).
func seedPoolType(t *testing.T, state, name, poolType string) {
	t.Helper()
	pool := "<pool type='" + poolType + "'><name>" + name + "</name>" +
		"<capacity unit='bytes'>107374182400</capacity>" +
		"<allocation unit='bytes'>1073741824</allocation>" +
		"<available unit='bytes'>106300440576</available>" +
		"<target><path>" + filepath.Join(state, "pool-"+name) + "</path></target></pool>"
	if err := os.WriteFile(filepath.Join(state, "pools", name+".xml"), []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "pools_active", name), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedDomain writes a domain XML + state file and a matching volume file.
func seedDomain(t *testing.T, state, name, stateVal, xml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(state, "domains", name+".xml"), []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "state", name), []byte(stateVal), 0o644); err != nil {
		t.Fatal(err)
	}
	vols := filepath.Join(state, "vols", "default")
	if err := os.MkdirAll(vols, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vols, "sc-"+name+"-root"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
}

func errIs(target error, err error) bool { return errors.Is(err, target) }

func TestNameAndLimitAllowlists(t *testing.T) {
	good := []string{"vm1", "web-01", "a", "vm-0123456789012345678901234567"}
	for _, n := range good {
		if !ValidVMName(n) {
			t.Errorf("ValidVMName(%q) = false, want true", n)
		}
	}
	bad := []string{"", "VM1", "1vm", "-vm", "vm_1", "vm.1", "vm;rm", "vm<br>",
		"../../etc", "a/b", "vm space", "vm-01234567890123456789012345678901"}
	for _, n := range bad {
		if ValidVMName(n) {
			t.Errorf("ValidVMName(%q) = true, want false", n)
		}
	}
	// Pool/snapshot/bridge boundaries.
	if !ValidPoolName("pool-1") || ValidPoolName("") || ValidPoolName("/etc") ||
		ValidPoolName(".hidden") || ValidPoolName("a b") {
		t.Error("pool allowlist sınırı hatalı")
	}
	if !ValidBridge("br0") || !ValidBridge("virbr0") || ValidBridge("") ||
		ValidBridge("verylongbridgename16") || ValidBridge("a/b") {
		t.Error("bridge allowlist sınırı hatalı")
	}
	if !ValidVCPU(1) || !ValidVCPU(64) || ValidVCPU(0) || ValidVCPU(65) {
		t.Error("vCPU sınırı hatalı")
	}
	if !ValidMemMiB(128) || !ValidMemMiB(256<<10) || ValidMemMiB(127) || ValidMemMiB(256<<10+1) {
		t.Error("bellek sınırı hatalı")
	}
	if !ValidDiskGiB(1) || !ValidDiskGiB(4096) || ValidDiskGiB(0) || ValidDiskGiB(4097) {
		t.Error("disk sınırı hatalı")
	}
	// Managed-volume namespace (criterion 3 gate).
	if !ManagedVolume("sc-vm1-root") || !ManagedVolume("sc-a") {
		t.Error("sc- önekli birim yönetilen sayılmalı")
	}
	for _, v := range []string{"", "sc-", "sca", "other-vm-disk", "sc-/../x", "sc-UPPER", "sc-vm space"} {
		if ManagedVolume(v) {
			t.Errorf("ManagedVolume(%q) = true, want false", v)
		}
	}
}

func TestPreflightReportOnly(t *testing.T) {
	r, _, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, shimsState(t, r), "default")
	seedBridge(t, r, "br0")
	if err := os.WriteFile(filepath.Join(shimsState(t, r), "networks"), []byte("default\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pf, err := r.Preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !pf.KVMAvailable || pf.VirtFlag != "vmx" {
		t.Errorf("kvm/virt flag hatalı: %+v", pf)
	}
	if pf.MemTotalMiB != 1024 || pf.MemAvailMiB != 512 {
		t.Errorf("meminfo parse hatalı: %d/%d", pf.MemTotalMiB, pf.MemAvailMiB)
	}
	if pf.LibvirtVer != "10.10.0" || pf.Hypervisor != "QEMU 8.2.0" {
		t.Errorf("version parse hatalı: %q %q", pf.LibvirtVer, pf.Hypervisor)
	}
	if len(pf.Pools) != 1 || pf.Pools[0].Name != "default" || pf.Pools[0].Type != "dir" {
		t.Errorf("pools parse hatalı: %+v", pf.Pools)
	}
	if pf.Pools[0].FreeBytes != 106300440576 {
		t.Errorf("pool free parse hatalı: %d", pf.Pools[0].FreeBytes)
	}
	if len(pf.Bridges) != 1 || pf.Bridges[0] != "br0" {
		t.Errorf("bridges parse hatalı: %+v", pf.Bridges)
	}
	if len(pf.Networks) != 1 || pf.Networks[0] != "default" {
		t.Errorf("networks parse hatalı: %+v", pf.Networks)
	}
	if !pf.QemuImg {
		t.Error("qemu-img bulunmalıydı")
	}
}

// shimsState recovers the state dir from the runner paths.
func shimsState(t *testing.T, r *Runner) string {
	t.Helper()
	return filepath.Dir(r.CPUInfoPath)
}

func TestPreflightWithoutKVMReportsHonestNote(t *testing.T) {
	r, _, _ := shims(t)
	seedHost(t, r, false)
	pf, err := r.Preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.KVMAvailable {
		t.Error("kvm yokken available=true olmamalı")
	}
	if len(pf.Notes) == 0 {
		t.Fatal("TCG sınırı dürüst notla raporlanmalı")
	}
	found := false
	for _, n := range pf.Notes {
		if strings.Contains(n, "TCG") || strings.Contains(n, "vmx/svm") {
			found = true
		}
	}
	if !found {
		t.Errorf("TCG notu yok: %v", pf.Notes)
	}
}

func TestListAndStatus(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	seedDomain(t, state, "vm2", "shut off", fixtureDomainXML)
	rows, err := r.List(context.Background(), 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("2 domain bekleniyordu: %+v", rows)
	}
	byName := map[string]VMRow{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	if !byName["vm1"].Running || byName["vm2"].Running {
		t.Errorf("state ayrımı hatalı: %+v", rows)
	}
	st, err := r.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.DomainsTotal != 2 || st.Running != 1 {
		t.Errorf("status sayıları hatalı: %+v", st)
	}
	if st.URI != "qemu:///test" {
		t.Errorf("URI hatalı: %q", st.URI)
	}
}

func TestInfoParsesGuestAgentAndConsole(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	// fmt default qcow2 → XML claim matches → format_verified true.
	info, err := r.Info(context.Background(), "vm1")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.UUID == "" || info.VCPU != 2 || info.MemMiB != 512 {
		t.Errorf("temel alanlar hatalı: %+v", info)
	}
	if !info.GuestAgent {
		t.Error("guest agent kanalı raporlanmalı (kriter 5)")
	}
	if !info.Console.LocalOnly || info.Console.GraphicsType != "vnc" || info.Console.Port != 5900 {
		t.Errorf("console metadata hatalı: %+v", info.Console)
	}
	if !info.Console.Serial {
		t.Error("serial console raporlanmalı")
	}
	if len(info.Disks) != 1 {
		t.Fatalf("1 disk bekleniyordu: %+v", info.Disks)
	}
	d := info.Disks[0]
	if !d.Managed || d.Volume != "sc-vm1-root" || d.Pool != "default" {
		t.Errorf("managed disk çözümü hatalı: %+v", d)
	}
	if !d.FormatVerified || d.ActualFormat != "qcow2" || d.XMLFormat != "qcow2" {
		t.Errorf("format doğrulaması hatalı: %+v", d)
	}
	if !d.CheckOK {
		t.Errorf("qcow2 check başarılı olmalı: %+v", d)
	}
	if len(info.Interfaces) != 1 || info.Interfaces[0].Bridge != "br0" ||
		info.Interfaces[0].MAC != "52:54:00:aa:bb:01" {
		t.Errorf("iface parse hatalı: %+v", info.Interfaces)
	}
}

func TestCreateLifecycleAndXMLShape(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, state, "default")
	seedBridge(t, r, "br0")
	res, err := r.Create(context.Background(), "web-01", 2, 512, 20, "default", "br0")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Name != "web-01" || res.VCPU != 2 || res.MemMiB != 512 || res.DiskGiB != 20 {
		t.Errorf("create sonucu hatalı: %+v", res)
	}
	if res.Volume != "sc-web-01-root" || res.Pool != "default" || res.BridgeKind != "bridge" {
		t.Errorf("yönetilen birim/köprü hatalı: %+v", res)
	}
	if res.Accelerator != "kvm" {
		t.Errorf("kvm mevcutken accelerator kvm olmalı: %+v", res)
	}
	// The defined XML is what the fake stored — inspect the real shape.
	defined, err := os.ReadFile(filepath.Join(state, "domains", "web-01.xml"))
	if err != nil {
		t.Fatalf("tanımlı domain okunamadı: %v", err)
	}
	xmlText := string(defined)
	for _, want := range []string{
		"<memory unit='MiB'>512</memory>", "<vcpu>2</vcpu>",
		"pool='default' volume='sc-web-01-root'", "listen='127.0.0.1'",
		"org.qemu.guest_agent.0", "type='kvm'",
	} {
		if !strings.Contains(xmlText, want) {
			t.Errorf("tanımlı XML %q içermeli:\n%s", want, xmlText)
		}
	}
	// The volume really exists in the state dir.
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-web-01-root")); err != nil {
		t.Errorf("yönetilen birim yaratılmalıydı: %v", err)
	}
}

func TestCreateRefusals(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, state, "default")
	seedBridge(t, r, "br0")
	ctx := context.Background()
	cases := []struct {
		name      string
		vmName    string
		vcpu      int
		mem, disk int64
		pool, br  string
		want      error
	}{
		{"kötü ad", "../evil", 2, 512, 20, "default", "br0", ErrInvalidName},
		{"büyük harf", "Web01", 2, 512, 20, "default", "br0", ErrInvalidName},
		{"vcpu üst", "vm-x", 65, 512, 20, "default", "br0", ErrInvalidLimits},
		{"bellek üst (payload tavan)", "vm-x", 2, 256<<10 + 1, 20, "default", "br0", ErrInvalidLimits},
		{"bellek alt", "vm-x", 2, 127, 20, "default", "br0", ErrInvalidLimits},
		{"disk üst", "vm-x", 2, 512, 4097, "default", "br0", ErrInvalidLimits},
		{"kötü pool", "vm-x", 2, 512, 20, "/etc/evil", "br0", ErrInvalidPool},
		{"kötü köprü", "vm-x", 2, 512, 20, "default", "a b", ErrInvalidBridge},
		{"pool yok", "vm-x", 2, 512, 20, "missing", "br0", ErrNotFound},
		{"köprü yok", "vm-x", 2, 512, 20, "default", "nonexistent0", ErrInvalidBridge},
	}
	for _, tc := range cases {
		if _, err := r.Create(ctx, tc.vmName, tc.vcpu, tc.mem, tc.disk, tc.pool, tc.br); !errIs(tc.want, err) {
			t.Errorf("%s: hata %v, want %v", tc.name, err, tc.want)
		}
	}
	// Host memory ceiling (criterion 2: honest host check).
	if err := os.WriteFile(r.MemInfoPath, []byte("MemTotal: 262144 kB\nMemAvailable: 131072 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(ctx, "vm-x", 2, 512, 20, "default", "br0"); !errIs(ErrInsufficient, err) {
		t.Errorf("host belleği yetersizken red bekleniyordu: %v", err)
	}
	if err := os.WriteFile(r.MemInfoPath, []byte("MemTotal: 1048576 kB\nMemAvailable: 524288 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Duplicate name.
	seedDomain(t, state, "vm-x", "shut off", fixtureDomainXML)
	if _, err := r.Create(ctx, "vm-x", 2, 512, 20, "default", "br0"); !errIs(ErrExists, err) {
		t.Errorf("mükerrer ad red bekleniyordu: %v", err)
	}
}

func TestCreateRollsBackVolumeOnDefineFailure(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, state, "default")
	seedBridge(t, r, "br0")
	t.Setenv("FAIL_DEFINE", "1")
	if _, err := r.Create(context.Background(), "vm-rollback", 2, 512, 20, "default", "br0"); !errIs(ErrConflict, err) {
		t.Fatalf("define başarısızlığında çelişki bekleniyordu: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-vm-rollback-root")); !os.IsNotExist(err) {
		t.Errorf("orphan birim geri alınmalıydı: %v", err)
	}
}

func TestStopGracefulTimeoutAndForce(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	ctx := context.Background()
	// No converge → typed conflict after the bounded window; never a silent destroy.
	if _, err := r.Stop(ctx, "vm1", false); !errIs(ErrConflict, err) {
		t.Fatalf("graceful zaman aşımı çelişki bekleniyordu: %v", err)
	}
	if st, _ := r.domstate(ctx, "vm1"); st != "running" {
		t.Errorf("graceful başarısızlıkta state değişmemeliydi: %s", st)
	}
	// Force → destroy (explicit caller decision).
	res, err := r.Stop(ctx, "vm1", true)
	if err != nil {
		t.Fatalf("force stop: %v", err)
	}
	if !res.Forced || res.State != "shut off" {
		t.Errorf("force stop sonucu hatalı: %+v", res)
	}
	// Already stopped → honest no-op.
	res2, err := r.Stop(ctx, "vm1", false)
	if err != nil || !res2.Already {
		t.Errorf("zaten kapalı no-op bekleniyordu: %+v %v", res2, err)
	}
}

func TestStartRebootStateGuards(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedDomain(t, state, "vm1", "shut off", fixtureDomainXML)
	ctx := context.Background()
	if _, err := r.Reboot(ctx, "vm1"); !errIs(ErrConflict, err) {
		t.Errorf("kapalı VM'de reboot red bekleniyordu: %v", err)
	}
	res, err := r.Start(ctx, "vm1")
	if err != nil || res.Already || res.State != "running" {
		t.Errorf("start sonucu hatalı: %+v %v", res, err)
	}
	res2, err := r.Start(ctx, "vm1")
	if err != nil || !res2.Already {
		t.Errorf("zaten çalışan no-op bekleniyordu: %+v %v", res2, err)
	}
	if _, err := r.Reboot(ctx, "vm1"); err != nil {
		t.Errorf("çalışan VM'de reboot başarılı olmalıydı: %v", err)
	}
}

func TestDeleteManagedGateAndLifecycle(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	ctx := context.Background()

	// Foreign FILE disk outside any pool → whole delete refused.
	foreign := strings.Replace(fixtureDomainXML,
		"<source pool='default' volume='sc-vm1-root'/>",
		"<source file='/var/lib/libvirt/images/foreign.qcow2'/>", 1)
	seedDomain(t, state, "vmf", "shut off", foreign)
	if _, err := r.Delete(ctx, "vmf"); !errIs(ErrForeignVolume, err) {
		t.Fatalf("yabancı dosya diski red bekleniyordu: %v", err)
	}
	if !r.exists(ctx, "vmf") {
		t.Error("red durumunda VM tanımlı kalmalı (state değişmedi)")
	}
	// Foreign VOLUME name (not sc-) → refused.
	foreignVol := strings.Replace(fixtureDomainXML,
		"volume='sc-vm1-root'", "volume='other-vm-disk'", 1)
	seedDomain(t, state, "vmg", "shut off", foreignVol)
	if _, err := r.Delete(ctx, "vmg"); !errIs(ErrForeignVolume, err) {
		t.Fatalf("yabancı birim adı red bekleniyordu: %v", err)
	}
	// Running VM → conflict.
	seedDomain(t, state, "vmrun", "running", fixtureDomainXML)
	if _, err := r.Delete(ctx, "vmrun"); !errIs(ErrConflict, err) {
		t.Fatalf("çalışan VM silme red bekleniyordu: %v", err)
	}
	// Managed delete: undefine + volume removal, auditable list.
	seedDomain(t, state, "vm1", "shut off", fixtureDomainXML)
	res, err := r.Delete(ctx, "vm1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.Undefined || len(res.VolumesRemoved) != 1 ||
		res.VolumesRemoved[0] != "default/sc-vm1-root" {
		t.Errorf("delete sonucu hatalı: %+v", res)
	}
	if r.exists(ctx, "vm1") {
		t.Error("VM tanımdan kalkmalıydı")
	}
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-vm1-root")); !os.IsNotExist(err) {
		t.Errorf("yönetilen birim silinmeliydi: %v", err)
	}
	// Second delete → typed not-found (idempotency is the caller's job).
	if _, err := r.Delete(ctx, "vm1"); !errIs(ErrNotFound, err) {
		t.Errorf("olmayan VM red bekleniyordu: %v", err)
	}
}

func TestDeleteRecoveryAfterPartialUndefine(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "shut off", fixtureDomainXML)
	// Undefine FAILS but the domain is actually gone (previous partial run).
	t.Setenv("FAIL_UNDEFINE", "1")
	t.Setenv("UNDEF_GONE", "1")
	res, err := r.Delete(context.Background(), "vm1")
	if err != nil {
		t.Fatalf("kismi silme sonrası kurtarma başarısız: %v", err)
	}
	if len(res.VolumesRemoved) != 1 {
		t.Errorf("kalan birimler silinmeliydi: %+v", res)
	}
}

func TestSnapshotQcow2Gate(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	ctx := context.Background()

	// REAL format raw (XML claims qcow2) → typed refusal (criterion 4).
	if err := os.WriteFile(filepath.Join(state, "fmt", "sc-vm1-root"), []byte("raw"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SnapshotCreate(ctx, "vm1", "snap1", "test"); !errIs(ErrDiskFormat, err) {
		t.Fatalf("raw disk snapshot red bekleniyordu: %v", err)
	}
	// Real qcow2 but XML claims raw → format mismatch refusal.
	if err := os.Remove(filepath.Join(state, "fmt", "sc-vm1-root")); err != nil {
		t.Fatal(err)
	}
	mismatch := strings.Replace(fixtureDomainXML, "type='qcow2'", "type='raw'", 1)
	if err := os.WriteFile(filepath.Join(state, "domains", "vm1.xml"), []byte(mismatch), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SnapshotCreate(ctx, "vm1", "snap1", "test"); !errIs(ErrFormatMismatch, err) {
		t.Fatalf("format uyuşmazlığı red bekleniyordu: %v", err)
	}
}

func TestSnapshotLifecycle(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	ctx := context.Background()
	res, err := r.SnapshotCreate(ctx, "vm1", "snap1", "temiz kurulum")
	if err != nil {
		t.Fatalf("snapshot create: %v", err)
	}
	if res.Name != "snap1" || len(res.VerifiedFormats) != 1 || res.VerifiedFormats[0] != "vda=qcow2" {
		t.Errorf("verified formatlar hatalı: %+v", res)
	}
	if _, err := r.SnapshotCreate(ctx, "vm1", "snap1", "dup"); !errIs(ErrExists, err) {
		t.Errorf("mükerrer snapshot red bekleniyordu: %v", err)
	}
	if err := os.WriteFile(filepath.Join(state, "current"), []byte("snap1"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := r.SnapshotList(ctx, "vm1")
	if err != nil || len(rows) != 1 || !rows[0].Current || rows[0].Name != "snap1" {
		t.Errorf("snapshot list hatalı: %+v %v", rows, err)
	}
	if _, err := r.SnapshotRevert(ctx, "vm1", "snap1"); err != nil {
		t.Errorf("revert: %v", err)
	}
	if _, err := r.SnapshotRevert(ctx, "vm1", "missing"); !errIs(ErrNotFound, err) {
		t.Errorf("olmayan snapshot revert red: %v", err)
	}
	if _, err := r.SnapshotDelete(ctx, "vm1", "snap1"); err != nil {
		t.Errorf("snapshot delete: %v", err)
	}
	if _, err := r.SnapshotDelete(ctx, "vm1", "snap1"); !errIs(ErrNotFound, err) {
		t.Errorf("ikinci delete red bekleniyordu: %v", err)
	}
}

func TestDiskAddLifecycle(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "running", fixtureDomainXML)
	ctx := context.Background()
	res, err := r.DiskAdd(ctx, "vm1", "default", 10)
	if err != nil {
		t.Fatalf("disk add: %v", err)
	}
	if res.TargetDev != "vdb" || res.Volume != "sc-vm1-vdb" || res.Pool != "default" {
		t.Errorf("disk add sonucu hatalı: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-vm1-vdb")); err != nil {
		t.Errorf("yeni birim yaratılmalıydı: %v", err)
	}
	// Missing pool.
	if _, err := r.DiskAdd(ctx, "vm1", "missing", 10); !errIs(ErrNotFound, err) {
		t.Errorf("olmayan pool red: %v", err)
	}
	// Attach failure on a FRESH domain → volume rolled back (vm1 already
	// owns sc-vm1-vdb from the successful call above).
	seedDomain(t, state, "vm2", "shut off", fixtureDomainXML)
	t.Setenv("FAIL_ATTACH", "1")
	if _, err := r.DiskAdd(ctx, "vm2", "default", 10); !errIs(ErrConflict, err) {
		t.Fatalf("attach başarısızlığı çelişki bekleniyordu: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-vm2-vdb")); !os.IsNotExist(err) {
		t.Errorf("orphan birim geri alınmalıydı: %v", err)
	}
}

func TestCloneRequiresStoppedSource(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, true)
	seedPool(t, state, "default")
	seedDomain(t, state, "src", "running", fixtureDomainXML)
	ctx := context.Background()
	if _, err := r.Clone(ctx, "src", "clone1"); !errIs(ErrConflict, err) {
		t.Fatalf("çalışan kaynak red bekleniyordu: %v", err)
	}
	seedDomain(t, state, "src", "shut off", fixtureDomainXML)
	res, err := r.Clone(ctx, "src", "clone1")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if res.Source != "src" || res.Name != "clone1" || res.Volume != "sc-clone1-vda" {
		t.Errorf("clone sonucu hatalı: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(state, "domains", "clone1.xml")); err != nil {
		t.Errorf("klon tanımlı olmalıydı: %v", err)
	}
	// New UUID must differ from the source (fresh identity).
	defined, _ := os.ReadFile(filepath.Join(state, "domains", "clone1.xml"))
	if strings.Contains(string(defined), "11111111-2222-3333-4444-555555555555") {
		t.Error("klon UUID'si kaynaktan farklı olmalı")
	}
	// Duplicate clone name.
	if _, err := r.Clone(ctx, "src", "clone1"); !errIs(ErrExists, err) {
		t.Errorf("mükerrer klon adı red: %v", err)
	}
	// Define failure → created volumes rolled back.
	t.Setenv("FAIL_DEFINE", "1")
	if _, err := r.Clone(ctx, "src", "clone2"); !errIs(ErrConflict, err) {
		t.Fatalf("define başarısızlığı çelişki bekleniyordu: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "vols", "default", "sc-clone2-vda")); !os.IsNotExist(err) {
		t.Errorf("klon birimleri geri alınmalıydı: %v", err)
	}
}

func TestDeleteRemovesSnapshotsBeforeUndefine(t *testing.T) {
	// Gerçek CI bulgusu (CI-46): libvirt, snapshot'ı olan inaktif domain'in
	// undefine'ını reddeder — delete önce snapshot'ları kaldırmalı.
	r, state, _ := shims(t)
	seedHost(t, r, false)
	seedPool(t, state, "default")
	seedDomain(t, state, "vm1", "shut off", fixtureDomainXML)
	snaps := filepath.Join(state, "snapshots", "vm1")
	if err := os.MkdirAll(snaps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snaps, "snap1"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := r.Delete(context.Background(), "vm1")
	if err != nil {
		t.Fatalf("snapshot'lı delete: %v", err)
	}
	if len(res.VolumesRemoved) != 1 {
		t.Errorf("birim silinmeliydi: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(snaps, "snap1")); !os.IsNotExist(err) {
		t.Errorf("snapshot silinmeliydi: %v", err)
	}
}

func TestCloneRefusesNonDirAnchorPool(t *testing.T) {
	r, state, _ := shims(t)
	seedHost(t, r, false)
	// Anchor pool is logical — qemu-img convert cannot place a file there;
	// the runner must refuse honestly instead of guessing a target.
	seedPoolType(t, state, "default", "logical")
	seedDomain(t, state, "src", "shut off", fixtureDomainXML)
	if _, err := r.Clone(context.Background(), "src", "clone1"); !errIs(ErrConflict, err) {
		t.Fatalf("dir olmayan havuzda klon red bekleniyordu: %v", err)
	}
}

func TestUnavailableWithoutVirsh(t *testing.T) {
	r, _, _ := shims(t)
	r.Virsh = filepath.Join(t.TempDir(), "missing-virsh")
	ctx := context.Background()
	if _, err := r.List(ctx, 0); !errIs(ErrUnavailable, err) {
		t.Errorf("virsh yokken ErrUnavailable bekleniyordu: %v", err)
	}
	if _, err := r.Status(ctx); !errIs(ErrUnavailable, err) {
		t.Errorf("virsh yokken ErrUnavailable bekleniyordu: %v", err)
	}
	if _, err := r.Start(ctx, "vm1"); !errIs(ErrUnavailable, err) {
		t.Errorf("virsh yokken ErrUnavailable bekleniyordu: %v", err)
	}
}
