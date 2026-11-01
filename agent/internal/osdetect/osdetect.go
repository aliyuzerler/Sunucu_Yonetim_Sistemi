// Package osdetect implements Faz 04/05 system fact detection. Detection is
// read-only; it never modifies the host. The Facts model is shared across
// operating systems; per-GOOS detection lives in detect_linux.go and
// detect_windows.go (ADR-0009).
package osdetect

import (
	"os"
	"time"
)

// Facts is the detected platform data reported to the control plane.
type Facts struct {
	OSID             string // linux: /etc/os-release ID (ubuntu, debian); windows: "windows"
	OSVersionID      string // linux: VERSION_ID; windows: DisplayVersion (e.g. 21H2)
	OSPrettyName     string // linux: PRETTY_NAME; windows: ProductName+edition
	OSHomeURL        string // linux: HOME_URL; windows: ""
	KernelRelease    string // linux: /proc/sys/kernel/osrelease; windows: "build 20348.230"
	Arch             string
	SystemdAvailable bool   // linux only (false on windows)
	SystemdVersion   string // linux only
	PackageManager   string // linux: apt|dpkg|dnf|yum|unknown; windows: msi|unknown
	Source           string // provenance, e.g. "os-release+procfs+systemctl" / "registry"
	CollectedAt      time.Time
}

// Source constants name the underlying data origins.
const (
	SourceOSRelease = "os-release"
	SourceProcfs    = "procfs"
	SourceSystemctl = "systemctl"
	SourceRegistry  = "registry"
)

// Probe reports systemd availability and version for the host at root
// (linux only; ignored on windows).
type Probe func(root string) (version string, ok bool)

// joinSource appends provenance fragments.
func joinSource(a, b string) string {
	if a == "" {
		return b
	}
	return a + "+" + b
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
