//go:build windows

package osdetect

import (
	"strings"
	"testing"
)

// TestDetectWindowsSmoke exercises the REAL registry read path on a Windows
// runner.
func TestDetectWindowsSmoke(t *testing.T) {
	facts, err := Detect("", nil)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if facts.OSID != "windows" {
		t.Fatalf("OSID: %q", facts.OSID)
	}
	if facts.OSPrettyName == "" || !strings.Contains(facts.OSPrettyName, "Windows") {
		t.Fatalf("ProductName: %q", facts.OSPrettyName)
	}
	if facts.Source != SourceRegistry {
		t.Fatalf("kaynak: %q", facts.Source)
	}
	if facts.SystemdAvailable {
		t.Fatal("windows'ta systemd false olmalı")
	}
	if facts.CollectedAt.IsZero() {
		t.Fatal("timestamp zorunlu")
	}
	t.Logf("facts: %s %s (%s)", facts.OSPrettyName, facts.OSVersionID, facts.KernelRelease)
}
