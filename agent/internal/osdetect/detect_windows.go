//go:build windows

package osdetect

import (
	"fmt"
	"runtime"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Detect collects Windows system facts from the registry (read-only).
// Reads HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion and reports
// ProductName/DisplayVersion/CurrentBuild+UBR. There is no systemd and no
// apt on Windows: SystemdAvailable=false, PackageManager="msi" when
// msiexec.exe exists, else "unknown" (ADR-0009).
func Detect(root string, probe Probe) (Facts, error) {
	_ = root  // fixture root only meaningful on linux tests
	_ = probe // systemd probe is linux-only
	f := Facts{Arch: runtime.GOARCH, CollectedAt: time.Now().UTC()}

	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return f, fmt.Errorf("registry okunamadı: %w", err)
	}
	defer k.Close()

	productName, _, _ := k.GetStringValue("ProductName")
	displayVersion, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	ubr, _, _ := k.GetIntegerValue("UBR")

	f.OSID = "windows"
	f.OSVersionID = displayVersion
	f.OSPrettyName = productName
	if build != "" {
		f.KernelRelease = fmt.Sprintf("build %s.%d", build, ubr)
	}
	if displayVersion == "" {
		f.OSVersionID = build // older hosts without DisplayVersion
	}
	f.PackageManager = detectWindowsPackageManager()
	f.Source = SourceRegistry
	if f.OSPrettyName == "" && build == "" {
		return f, fmt.Errorf("sistem bilgileri okunamadı")
	}
	return f, nil
}

// detectWindowsPackageManager reports "msi" when the Windows Installer
// service binary exists. There is deliberately no winget/chocolatey probe:
// agent capabilities never INSTALL anything (Faz 05 scope).
func detectWindowsPackageManager() string {
	if fileExists(`C:\Windows\System32\msiexec.exe`) {
		return "msi"
	}
	return "unknown"
}
