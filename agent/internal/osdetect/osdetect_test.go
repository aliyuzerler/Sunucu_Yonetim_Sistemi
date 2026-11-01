//go:build linux

package osdetect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture matrix covers the two primary distros (docs/phases/phase-04.md:
// "iki distro test matrisi").
func TestDetectUbuntu2404(t *testing.T) {
	f, err := Detect("testdata/ubuntu-24.04", func(string) (string, bool) {
		return "", false // fixture systemd probed separately via DefaultProbe below
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if f.OSID != "ubuntu" || f.OSVersionID != "24.04" {
		t.Fatalf("ubuntu kimliği beklenir, geldi: %s %s", f.OSID, f.OSVersionID)
	}
	if f.OSPrettyName != "Ubuntu 24.04.3 LTS" {
		t.Fatalf("PRETTY_NAME: %q", f.OSPrettyName)
	}
	if f.OSHomeURL != "https://www.ubuntu.com/" {
		t.Fatalf("HOME_URL: %q", f.OSHomeURL)
	}
	if f.KernelRelease != "6.8.0-49-generic" {
		t.Fatalf("kernel: %q", f.KernelRelease)
	}
	if f.PackageManager != "apt" {
		t.Fatalf("paket yöneticisi apt beklenir: %s", f.PackageManager)
	}
	if f.SystemdAvailable {
		t.Fatalf("probe nil ile systemd false olmalı")
	}
	if !strings.Contains(f.Source, "os-release") || !strings.Contains(f.Source, "procfs") {
		t.Fatalf("kaynak işaretleri eksik: %s", f.Source)
	}
	if f.CollectedAt.IsZero() {
		t.Fatalf("timestamp zorunlu")
	}
}

func TestDetectDebian13(t *testing.T) {
	f, err := Detect("testdata/debian-13", nil)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if f.OSID != "debian" || f.OSVersionID != "13" {
		t.Fatalf("debian kimliği beklenir: %s %s", f.OSID, f.OSVersionID)
	}
	if f.OSPrettyName != "Debian GNU/Linux 13 (trixie)" {
		t.Fatalf("PRETTY_NAME: %q", f.OSPrettyName)
	}
	if !f.SystemdAvailable {
		t.Fatalf("fixture systemd çalışıyor olmalı")
	}
	// DefaultProbe: run/systemd/system yok ama /proc/1/comm systemd der.
	// Fake systemctl PATH'e konur (test ortamı; üretim kodu asla shell çalıştırmaz).
	dir := t.TempDir()
	fake := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'systemd 257 (257.2-2)'\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	v, ok := DefaultProbe("testdata/debian-13")
	if !ok || v != "257" {
		t.Fatalf("Debian 13 systemd 257 beklenir: %q %v", v, ok)
	}
}

func TestParseSystemdVersion(t *testing.T) {
	cases := map[string]string{
		"systemd 255 (255.4-1ubuntu8.4)\n+PAM +AUDIT": "255",
		"systemd 257": "257",
		"init 1.0":    "",
		"":            "",
	}
	for in, want := range cases {
		if got := parseSystemdVersion(in); got != want {
			t.Errorf("parseSystemdVersion(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestOSReleaseQuoting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "os-release")
	content := "PRETTY_NAME=\"Say \\\"quoted\\\" 1.0\"\nID=test\nEMPTY=\n# comment\nNOEQUALS\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := parseOSRelease(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["PRETTY_NAME"] != `Say "quoted" 1.0` {
		t.Fatalf("escape çözümü: %q", m["PRETTY_NAME"])
	}
	if _, ok := m["NOEQUALS"]; ok {
		t.Fatalf("= içermeyen satır yok sayılmalı")
	}
}

func TestDetectUnknownHost(t *testing.T) {
	dir := t.TempDir() // boş kök: hiçbir kaynak yok
	if _, err := Detect(dir, nil); err == nil {
		t.Fatalf("boş host hata vermeli")
	}
	// Sadece procfs varsa hata yok; kaynak procfs olmalı.
	if err := os.MkdirAll(filepath.Join(dir, "proc", "sys", "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proc", "sys", "kernel", "osrelease"), []byte("6.1.0-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Detect(dir, nil)
	if err != nil {
		t.Fatalf("procfs tek başına yeterli: %v", err)
	}
	if f.KernelRelease != "6.1.0-x" || f.PackageManager != "unknown" {
		t.Fatalf("beklenmeyen sonuç: %+v", f)
	}
}

// Non-Linux fixture root: package manager detection must not find anything.
func TestPackageManagerUnknownWhenMissing(t *testing.T) {
	if got := detectPackageManager(t.TempDir()); got != "unknown" {
		t.Fatalf("unknown beklenir: %s", got)
	}
}
