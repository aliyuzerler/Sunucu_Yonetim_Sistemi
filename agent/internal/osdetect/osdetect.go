// Package osdetect implements Faz 04/05 system fact detection. Detection is
// read-only; it never modifies the host. The Facts model is shared across
// operating systems; per-GOOS detection lives in detect_linux.go and
// detect_windows.go (ADR-0009).
package osdetect

import (
	"os"
	"strings"
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
	DistroFamily     string // Faz 06: deb|rhel|debian-like|rhel-like|windows|unknown
	DistroLike       string // Faz 06: raw ID_LIKE value ("" when absent)
	SupportState     string // Faz 06: supported|untested|unknown (LinuxDistros matrisi)
	SupportReason    string // Faz 06: insan-okur gerekçe (UI rozetinde gösterilir)
	Source           string // provenance, e.g. "os-release+procfs+systemctl" / "registry"
	CollectedAt      time.Time
}

// Faz 06 support states (docs/architecture/linux-support.md).
const (
	SupportSupported = "supported" // listed major, CI-canonical evidence
	SupportUntested  = "untested"  // known distro, major NOT in the supported list
	SupportUnknown   = "unknown"   // distro not in the matrix at all
)

// SupportedDistros is the EXPLICIT support matrix (criterion: supported
// majors must be listed explicitly; untested versions must be reported as
// not supported). Key = os-release ID, value = supported MAJOR versions.
// A distro known here but with an unlisted major → "untested"; a distro not
// here → "unknown" (never a false positive: ID_LIKE alone never yields
// supported).
var SupportedDistros = map[string][]string{
	"ubuntu":    {"22", "24"},
	"debian":    {"12", "13"},
	"rocky":     {"9", "10"},
	"almalinux": {"9", "10"},
}

// DistroFamilies maps matrix IDs to the adapter family (service manager and
// package tooling selection depend on it).
var DistroFamilies = map[string]string{
	"ubuntu":    "deb",
	"debian":    "deb",
	"rocky":     "rhel",
	"almalinux": "rhel",
}

// ClassifyDistro derives (family, like, state, reason) from os-release
// fields. Pure function; fixture-tested on every GOOS (Faz 06 criterion:
// detection must NOT produce false positives).
//
// Rules:
//   - family comes from the ID first (DistroFamilies); only when the ID is
//     unknown does ID_LIKE suggest a coarse "debian-like"/"rhel-like" label
//     — a like-label NEVER yields "supported".
//   - state=supported ONLY for a matrix ID whose VERSION_ID major is listed.
//   - major parsing is strict: VERSION_ID "9.5"→"9", "24.04"→"24"; empty or
//     non-numeric majors never match.
func ClassifyDistro(id, versionID, idLike string) (family, state, reason string) {
	if fam, ok := DistroFamilies[id]; ok {
		majors := SupportedDistros[id]
		major := majorOf(versionID)
		for _, m := range majors {
			if m == major {
				return fam, SupportSupported, "desteklenen majör sürüm " + major
			}
		}
		if major == "" {
			return fam, SupportUntested, "sürüm bilgisi okunamadı (VERSION_ID boş/geçersiz)"
		}
		return fam, SupportUntested, "denenmemiş majör sürüm " + major + " — desteklenmiyor olarak raporlanır"
	}
	// Unknown ID: ID_LIKE only suggests a coarse family label.
	if like := likeFamily(idLike); like != "" {
		return like, SupportUnknown, "bilinmeyen dağıtım (ID=" + id + ", ID_LIKE=" + idLike + ") — destek iddiası yok"
	}
	if id == "windows" {
		return "windows", SupportSupported, "windows ailesi ayrı matris (windows-support.md)"
	}
	return "unknown", SupportUnknown, "bilinmeyen dağıtım (ID=" + id + ")"
}

// majorOf extracts the strict numeric major from VERSION_ID ("9.5"→"9",
// "24.04"→"24"); "" when the value has no numeric major.
func majorOf(versionID string) string {
	v := versionID
	for i := 0; i < len(v); i++ {
		if v[i] == '.' || v[i] == '-' || v[i] == '+' || v[i] == '~' {
			v = v[:i]
			break
		}
	}
	if v == "" {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return ""
		}
	}
	return v
}

// likeFamily maps the FIRST recognized token of ID_LIKE to a coarse family
// label. Unknown IDs keep "unknown" semantics.
func likeFamily(idLike string) string {
	for _, tok := range strings.Fields(idLike) {
		switch tok {
		case "debian", "ubuntu":
			return "debian-like"
		case "rhel", "centos", "fedora", "rocky", "almalinux":
			return "rhel-like"
		}
	}
	return ""
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
