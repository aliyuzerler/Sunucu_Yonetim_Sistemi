//go:build windows

package metrics

import (
	"testing"
)

// TestCollectWindowsSmoke exercises the real Win32 collection path on a
// Windows runner (CI). Read-only; asserts shape and bounds only.
func TestCollectWindowsSmoke(t *testing.T) {
	snap, err := NewCollector("").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.CollectedAt.IsZero() {
		t.Fatal("timestamp zorunlu")
	}
	if snap.Memory.TotalBytes == 0 {
		t.Fatal("GlobalMemoryStatusEx: TotalPhys > 0 olmalı")
	}
	if len(snap.Disks) == 0 {
		t.Fatal("fixed volume beklenir (C:\\)")
	}
	if snap.UptimeSeconds <= 0 {
		t.Fatal("uptime > 0 olmalı")
	}
	for _, d := range snap.Disks {
		if d.Device == "" || len(d.Device) != 3 {
			t.Errorf("volume root biçimi: %+v", d)
		}
	}
}
