//go:build linux

package osdetect

import (
	"os"
	"testing"
)

// TestRealHostSmoke runs against the REAL host it executes on. It is
// invoked explicitly by the CI agent-rhel job inside disposable Rocky
// Linux 9 / AlmaLinux 9 systemd containers (Faz 06 real-distro evidence).
// On any other host it FAILS LOUDLY rather than skipping silently: the
// job exists to prove the support claim, and a silent skip would prove
// nothing. It is read-only end to end (never modifies the host).
func TestRealHostSmoke(t *testing.T) {
	id := os.Getenv("EXPECTED_OS_ID")
	if id == "" {
		t.Skip("EXPECTED_OS_ID verilmedi (yalnız CI agent-rhel job'ında koşar)")
	}
	f, err := Detect("/", nil)
	if err != nil {
		t.Fatalf("Detect(/): %v", err)
	}
	t.Logf("gerçek host: id=%s version=%s pretty=%s pkg=%s systemd=%v family=%s support=%s (%s)",
		f.OSID, f.OSVersionID, f.OSPrettyName, f.PackageManager,
		f.SystemdAvailable, f.DistroFamily, f.SupportState, f.SupportReason)

	if f.OSID != id {
		t.Fatalf("os_id = %q, beklenen %q (yanlış container? gerçek kanıt yok)", f.OSID, id)
	}
	if f.SupportState != SupportSupported {
		t.Fatalf("desteklenmeyen dağıtım raporlandı: %s (%s) — matris/kod hatası", f.SupportState, f.SupportReason)
	}
	if f.DistroFamily != "rhel" {
		t.Fatalf("aile rhel beklenir: %s", f.DistroFamily)
	}
	if f.PackageManager != "dnf" {
		t.Fatalf("paket yöneticisi dnf beklenir: %s", f.PackageManager)
	}
	if !f.SystemdAvailable {
		t.Fatalf("systemd çalışıyor olmalı (privileged container)")
	}
	if f.KernelRelease == "" || f.Source == "" {
		t.Fatalf("çekirdek/kaynak provenansı eksik: %q %q", f.KernelRelease, f.Source)
	}
}
