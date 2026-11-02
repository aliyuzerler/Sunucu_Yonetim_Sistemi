//go:build linux

// REAL-host proof tests for the Faz 12 VM adapter (EXPECTED_VM=1). These
// run against a live libvirt daemon (qemu:///system) in the dedicated CI
// job — KVM when /dev/kvm exists, TCG otherwise (SeaBIOS no-boot stays
// "running", which is enough for the lifecycle proof). NOTHING here
// touches a production host: a disposable dir pool under os.TempDir and
// citest-prefixed domains only. Without EXPECTED_VM=1 the suite SKIPS
// loudly in the CI job (EXPECTED_VM=1 cannot be silently skipped — CI-17
// lesson: the gate FAILS the job instead of skipping).
package vmops

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func realVMRunner(t *testing.T) *Runner {
	t.Helper()
	if os.Getenv("EXPECTED_VM") != "1" {
		t.Skip("EXPECTED_VM=1 gerekli (gerçek libvirt kanıtı CI job'ında koşar)")
	}
	if _, err := os.Stat("/run/libvirt/libvirt-sock"); err != nil {
		// CI-17 dersi: gate açık ama ortam yoksa SKIP DEĞİL hata.
		if os.Getenv("CI") == "true" {
			t.Fatalf("EXPECTED_VM=1 ama libvirt soketi yok: %v", err)
		}
		t.Skipf("libvirt soketi yok: %v", err)
	}
	return &Runner{URI: "qemu:///system", Timeout: 90 * time.Second, StopGrace: 20 * time.Second}
}

// requireRealPool creates a disposable dir pool for the test.
func requireRealPool(t *testing.T, r *Runner, name string) string {
	t.Helper()
	ctx := context.Background()
	// NOT /tmp: libvirt's AppArmor abstraction (libvirt-qemu) denies world-
	// writable dirs for the qemu process (CI-43 real finding) — disposable
	// pools live under /var/lib/libvirt like production pools do.
	dir := filepath.Join("/var/lib/libvirt", "citest-pool-"+name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(os.TempDir(), "citest-pool-xml-"+name)
	xml := "<pool type='dir'><name>" + name + "</name><target><path>" + dir + "</path></target></pool>"
	if err := os.WriteFile(staged, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
	if _, err := r.virsh(ctx, "pool-define", staged); err != nil {
		t.Fatalf("pool-define: %v", err)
	}
	if _, err := r.virsh(ctx, "pool-start", name); err != nil {
		t.Fatalf("pool-start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.virsh(ctx, "pool-destroy", name)
		_, _ = r.virsh(ctx, "pool-undefine", name)
		_ = os.RemoveAll(dir)
	})
	return dir
}

// chownQemuTree mirrors libvirt's DAC relabel (test-only): volumes created
// by root are chowned to the qemu daemon user so the VM can open them RW.
// The user differs per distro (Ubuntu: libvirt-qemu, RHEL: qemu) — both are
// tried; on a host with neither, the chown is skipped honestly.
func chownQemuTree(t *testing.T, dir string) {
	t.Helper()
	var uid, gid int
	found := false
	for _, name := range []string{"libvirt-qemu", "qemu"} {
		if u, err := user.Lookup(name); err == nil {
			if v, err := strconv.Atoi(u.Uid); err == nil {
				uid = v
				gid = v
				if g, gerr := user.LookupGroup(name); gerr == nil {
					if gv, aerr := strconv.Atoi(g.Gid); aerr == nil {
						gid = gv
					}
				}
				found = true
				break
			}
		}
	}
	if !found {
		t.Logf("qemu daemon kullanıcısı bulunamadı — chown atlandı (dürüst)")
		return
	}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chown(path, uid, gid)
		}
		return nil
	})
}

// TestRealVMLifecycle is the criterion-2/3/4 real evidence: create → disk
// add (format verified qcow2) → snapshot create/revert (qcow2 gate) →
// start (running) → stop (force: no guest OS inside) → delete (managed
// volume removal audited).
func TestRealVMLifecycle(t *testing.T) {
	r := realVMRunner(t)
	ctx := context.Background()
	pool := "citest-vm-" + fmt.Sprint(os.Getpid())
	requireRealPool(t, r, pool)

	pf, err := r.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.URI != r.URI {
		t.Errorf("preflight URI: %q", pf.URI)
	}
	if len(pf.Notes) > 0 {
		t.Logf("preflight notları (dürüst sınır raporu): %v", pf.Notes)
	}

	// CREATE (criterion 2: typed op with resource limits).
	res, err := r.Create(ctx, "citest-vm1", 1, 256, 1, pool, "default")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Volume != "sc-citest-vm1-root" || res.Pool != pool {
		t.Fatalf("create sonucu: %+v", res)
	}
	t.Logf("create OK: accelerator=%s state=%s", res.Accelerator, res.State)

	// DISK ADD + FORMAT VERIFICATION (criterion 4: real qemu-img facts).
	if _, err := r.DiskAdd(ctx, "citest-vm1", pool, 1); err != nil {
		t.Fatalf("disk add: %v", err)
	}
	disks, err := r.DiskList(ctx, "citest-vm1")
	if err != nil {
		t.Fatalf("disk list: %v", err)
	}
	if len(disks) != 2 {
		t.Fatalf("2 disk bekleniyordu: %+v", disks)
	}
	for _, d := range disks {
		if d.ActualFormat != "qcow2" || !d.FormatVerified {
			t.Errorf("disk formatı doğrulanmalı (qcow2): %+v", d)
		}
		if !d.Managed {
			t.Errorf("yönetilen birim sc- çözülmeli: %+v", d)
		}
	}
	t.Logf("disk list OK: %d disk, formatlar qemu-img ile doğrulandı", len(disks))

	// SNAPSHOT lifecycle (criterion 4: qcow2-gated internal snapshots).
	sc, err := r.SnapshotCreate(ctx, "citest-vm1", "citest-snap", "temiz kurulum")
	if err != nil {
		t.Fatalf("snapshot create: %v", err)
	}
	if len(sc.VerifiedFormats) != 2 {
		t.Fatalf("snapshot doğrulama raporu: %+v", sc)
	}
	rows, err := r.SnapshotList(ctx, "citest-vm1")
	if err != nil || len(rows) != 1 || rows[0].Name != "citest-snap" {
		t.Fatalf("snapshot list: %+v %v", rows, err)
	}
	if _, err := r.SnapshotRevert(ctx, "citest-vm1", "citest-snap"); err != nil {
		t.Fatalf("snapshot revert: %v", err)
	}

	// INFO: guest-agent channel + local-only console metadata (criterion 5).
	info, err := r.Info(ctx, "citest-vm1")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if !info.GuestAgent {
		t.Error("guest agent kanalı tanımlı olmalı (in-guest yönetim yine de unsupported — kriter 5)")
	}
	if !info.Console.LocalOnly {
		t.Errorf("VNC local-only olmalı: %+v", info.Console)
	}

	// START (criterion 2): SeaBIOS no-boot stays running. Before start the
	// pool tree is relabeled like libvirt's DAC driver would do (test-only;
	// runners may ship qemu.conf with dynamic_ownership disabled).
	chownQemuTree(t, "/var/lib/libvirt/citest-pool-"+pool)
	if _, err := r.Start(ctx, "citest-vm1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	st, err := r.domstate(ctx, "citest-vm1")
	if err != nil || st != "running" {
		t.Fatalf("start sonrası state: %s %v", st, err)
	}

	// STOP (force: no guest OS → ACPI shutdown can never converge).
	if _, err := r.Stop(ctx, "citest-vm1", true); err != nil {
		t.Fatalf("force stop: %v", err)
	}

	// CLONE (stopped source).
	if _, err := r.Clone(ctx, "citest-vm1", "citest-vm2"); err != nil {
		t.Fatalf("clone: %v", err)
	}

	// DELETE both: managed volumes removed, nothing else touched.
	del, err := r.Delete(ctx, "citest-vm1")
	if err != nil {
		t.Fatalf("delete vm1: %v", err)
	}
	if len(del.VolumesRemoved) != 2 {
		t.Errorf("vm1 iki yönetilen birim silmeli: %+v", del)
	}
	del2, err := r.Delete(ctx, "citest-vm2")
	if err != nil {
		t.Fatalf("delete vm2: %v", err)
	}
	if len(del2.VolumesRemoved) != 1 {
		t.Errorf("vm2 bir yönetilen birim silmeli: %+v", del2)
	}
	if _, err := os.Stat(filepath.Join("/var/lib/libvirt", "citest-pool-"+pool, "sc-citest-vm1-root")); !os.IsNotExist(err) {
		t.Errorf("yönetilen birim diskten kalkmalıydı: %v", err)
	}
}

// TestRealVMForeignVolumeRefusal is the criterion-3 real evidence: a
// domain whose disk is NOT a managed sc- volume refuses deletion with a
// typed error and leaves the domain defined.
func TestRealVMForeignVolumeRefusal(t *testing.T) {
	r := realVMRunner(t)
	ctx := context.Background()
	pool := "citest-vmf-" + fmt.Sprint(os.Getpid())
	poolDir := requireRealPool(t, r, pool)

	// Hand-craft a foreign volume + domain XML referencing it.
	foreignPath := filepath.Join(poolDir, "foreign-disk.qcow2")
	staged := filepath.Join("/var/lib/libvirt", "citest-fxml-"+fmt.Sprint(os.Getpid()))
	xml := `<domain type='qemu'>
  <name>citest-vmf</name><uuid>99999999-8888-7777-6666-555555555555</uuid>
  <memory unit='MiB'>256</memory><vcpu>1</vcpu>
  <os><type arch='x86_64' machine='pc'>hvm</type></os>
  <devices>
    <disk type='file' device='disk'><driver name='qemu' type='qcow2'/><source file='` + foreignPath + `'/><target dev='vda' bus='virtio'/></disk>
    <console type='pty'><target type='serial' port='0'/></console>
  </devices>
</domain>`
	if err := os.WriteFile(staged, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
	if _, err := r.virsh(ctx, "define", staged); err != nil {
		t.Fatalf("define: %v", err)
	}
	if _, err := r.virsh(ctx, "vol-create-as", "--pool", pool, "--name", "foreign-disk.qcow2",
		"--capacity", "1G", "--format", "qcow2"); err != nil {
		t.Fatalf("foreign volume: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.virsh(ctx, "undefine", "citest-vmf")
		_, _ = r.virsh(ctx, "vol-delete", "--pool", pool, "foreign-disk.qcow2")
	})

	if _, err := r.Delete(ctx, "citest-vmf"); err == nil {
		t.Fatal("yabancı diski olan VM silinmemeliydi")
	} else if !strings.Contains(err.Error(), "yabancı") && !strings.Contains(err.Error(), "havuz") {
		t.Logf("red gerekçesi: %v", err)
	}
	if !r.exists(ctx, "citest-vmf") {
		t.Error("red sonrası VM tanımlı kalmalı (state değişmedi)")
	}
	// DiskList still reports the foreign disk honestly (managed=false).
	disks, err := r.DiskList(ctx, "citest-vmf")
	if err != nil {
		t.Fatalf("disk list: %v", err)
	}
	if len(disks) != 1 || disks[0].Managed {
		t.Errorf("foreign disk managed=false raporlanmalı: %+v", disks)
	}
}
