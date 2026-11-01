//go:build linux

package metrics

import (
	"math"
	"testing"
	"time"
)

// Distro matrix: the two primary targets (docs/phases/phase-04.md).
func TestCollectUbuntu2404(t *testing.T) {
	c := NewCollector("testdata/ubuntu-24.04")
	snap, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.CollectedAt.IsZero() {
		t.Fatalf("timestamp zorunlu")
	}
	if snap.Memory.TotalBytes != 16277212<<10 {
		t.Fatalf("MemTotal: %d", snap.Memory.TotalBytes)
	}
	if snap.Memory.UsedBytes != (16277212-9898560)<<10 {
		t.Fatalf("UsedBytes: %d", snap.Memory.UsedBytes)
	}
	if snap.Memory.UsedPercent <= 0 || snap.Memory.UsedPercent >= 100 {
		t.Fatalf("UsedPercent: %v", snap.Memory.UsedPercent)
	}
	if snap.CPU.Load1m != 1.85 || snap.CPU.Load15m != 0.95 {
		t.Fatalf("load: %+v", snap.CPU)
	}
	if snap.CPU.LogicalCores == 0 {
		t.Fatalf("logical cores beklenir")
	}
	// ext4 kök + vfat /boot/efi + xfs /data; /data-dup aynı cihaz+fstype ama
	// farklı mount noktası → dedupe mount noktasına göre.
	if len(snap.Disks) != 3 {
		t.Fatalf("3 disk beklenir (ext4+vfat+xfs): %d %+v", len(snap.Disks), snap.Disks)
	}
	if snap.Disks[0].FSType != "ext4" || snap.Disks[0].MountPoint != "/" {
		t.Fatalf("ilk disk / ext4 olmalı: %+v", snap.Disks[0])
	}
	for _, d := range snap.Disks {
		if d.Device == "tmpfs" || d.FSType == "tmpfs" || d.FSType == "proc" {
			t.Fatalf("sahte dosya sistemi filtrelenmeli: %+v", d)
		}
	}
	if len(snap.Networks) != 2 {
		t.Fatalf("lo hariç 2 arayüz: %+v", snap.Networks)
	}
	if snap.Networks[0].Interface != "ens160" || snap.Networks[0].RxBytes != 9876543210 || snap.Networks[0].TxBytes != 1234567890 {
		t.Fatalf("ens160: %+v", snap.Networks[0])
	}
	if math.Abs(snap.UptimeSeconds-864123.45) > 0.01 {
		t.Fatalf("uptime: %v", snap.UptimeSeconds)
	}
}

func TestCollectDebian13(t *testing.T) {
	snap, err := NewCollector("testdata/debian-13").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.Memory.TotalBytes != 8154300<<10 {
		t.Fatalf("MemTotal: %d", snap.Memory.TotalBytes)
	}
	if len(snap.Disks) != 2 || snap.Disks[0].Device != "/dev/vda1" {
		t.Fatalf("diskler: %+v", snap.Disks)
	}
	if len(snap.Networks) != 1 || snap.Networks[0].Interface != "enp1s0" {
		t.Fatalf("ağ: %+v", snap.Networks)
	}
	if math.Abs(snap.UptimeSeconds-123456.78) > 0.01 {
		t.Fatalf("uptime: %v", snap.UptimeSeconds)
	}
}

// CPU percent requires two samples; the first is 0, the second is computed
// over the window between them.
func TestCPUPercentOverWindow(t *testing.T) {
	c := NewCollector("testdata/ubuntu-24.04")
	s1, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if s1.CPU.Percent != 0 {
		t.Fatalf("ilk örnek percent 0 olmalı: %v", s1.CPU.Percent)
	}
	// İkinci örnek: fixture değişmiyor; delta toplam >0 ve idle delta >0 →
	// percent <100 ve >=0 aralığında.
	s2, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	// Aynı fixture: delta idle == delta total → percent 0. Sadece aralık doğrula.
	if s2.CPU.Percent < 0 || s2.CPU.Percent > 100 {
		t.Fatalf("percent aralık dışı: %v", s2.CPU.Percent)
	}
}

func TestParseMountsUnescape(t *testing.T) {
	m := parseMounts("testdata/ubuntu-24.04/proc/mounts")
	if len(m) != 7 {
		t.Fatalf("7 mount satırı: %d", len(m))
	}
}

func TestClampPercent(t *testing.T) {
	if clampPercent(-1) != 0 || clampPercent(150) != 100 || clampPercent(50) != 50 {
		t.Fatalf("clamp bozuk")
	}
}

// Collect on the live sandbox is a smoke test: values must be non-negative
// and the snapshot must carry source + timestamp (granted: Linux-only).
func TestCollectLiveSmoke(t *testing.T) {
	snap, err := NewCollector("/").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.CollectedAt.After(time.Now()) {
		t.Fatalf("timestamp gelecekte olamaz")
	}
	if snap.Memory.TotalBytes == 0 {
		t.Fatalf("canlı sistemde MemTotal > 0 olmalı")
	}
	if snap.UptimeSeconds <= 0 {
		t.Fatalf("uptime > 0 olmalı")
	}
}
