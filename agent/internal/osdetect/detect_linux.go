//go:build linux

package osdetect

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Detect collects Linux system facts under root. probe may be nil
// (DefaultProbe).
func Detect(root string, probe Probe) (Facts, error) {
	if probe == nil {
		probe = DefaultProbe
	}
	f := Facts{Arch: runtime.GOARCH, CollectedAt: time.Now().UTC()}

	release, err := parseOSRelease(filepath.Join(root, "etc", "os-release"))
	if err == nil {
		f.OSID = release["ID"]
		f.OSVersionID = release["VERSION_ID"]
		f.OSPrettyName = release["PRETTY_NAME"]
		f.OSHomeURL = release["HOME_URL"]
		f.DistroLike = release["ID_LIKE"]
		f.Source = SourceOSRelease
	}
	// Faz 06: distro family + explicit support matrix (false-positive
	// guard: ID_LIKE alone never yields supported).
	f.DistroFamily, f.SupportState, f.SupportReason = ClassifyDistro(f.OSID, f.OSVersionID, f.DistroLike)
	if kr, err := os.ReadFile(filepath.Join(root, "proc", "sys", "kernel", "osrelease")); err == nil {
		f.KernelRelease = strings.TrimSpace(string(kr))
		f.Source = joinSource(f.Source, SourceProcfs)
	}
	if v, ok := probe(root); ok {
		f.SystemdAvailable = true
		f.SystemdVersion = v
		f.Source = joinSource(f.Source, SourceSystemctl)
	}
	f.PackageManager = detectPackageManager(root)
	if f.Source == "" {
		return f, fmt.Errorf("sistem bilgileri okunamadı: %s", root)
	}
	return f, nil
}

// parseOSRelease parses KEY=VALUE lines honoring quotes and escapes
// (os-release(5) shell-compatible quoting).
func parseOSRelease(path string) (map[string]string, error) {
	fh, err := os.Open(path) //nolint:gosec // path is root-joined by Detect or a test fixture
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
			v = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`).Replace(v)
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DefaultProbe determines systemd availability without modifying the host:
// /run/systemd/system presence or PID 1 identity, then `systemctl --version`
// (exec argv, never a shell) for the version number.
func DefaultProbe(root string) (string, bool) {
	if !systemdRunning(root) {
		return "", false
	}
	bin := lookPath(filepath.Join(root, "usr", "bin", "systemctl"), "systemctl")
	if bin == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runArgv(ctx, bin, "--version")
	if err != nil {
		return "", false
	}
	return parseSystemdVersion(out), true
}

// systemdRunning checks the two canonical markers (root-relative so that
// fixtures can fake both).
func systemdRunning(root string) bool {
	if st, err := os.Stat(filepath.Join(root, "run", "systemd", "system")); err == nil && st.IsDir() {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(root, "proc", "1", "comm")); err == nil {
		if strings.HasPrefix(strings.TrimSpace(string(b)), "systemd") {
			return true
		}
	}
	return false
}

// parseSystemdVersion extracts the version from `systemd 255 (2024.02)…`.
func parseSystemdVersion(firstLine string) string {
	fields := strings.Fields(strings.TrimSpace(firstLine))
	if len(fields) >= 2 && fields[0] == "systemd" {
		return fields[1]
	}
	return ""
}

// detectPackageManager probes well-known package managers under <root>/usr/bin.
// apt implies dpkg on Debian-family systems; report the highest-level tool.
func detectPackageManager(root string) string {
	for _, p := range []struct{ file, name string }{
		{"usr/bin/apt-get", "apt"},
		{"usr/bin/dnf", "dnf"},
		{"usr/bin/yum", "yum"},
		{"usr/bin/dpkg", "dpkg"},
	} {
		if fileExists(filepath.Join(root, p.file)) {
			return p.name
		}
	}
	return "unknown"
}

// lookPath prefers the root-relative well-known location, then PATH.
func lookPath(absolute, name string) string {
	if fileExists(absolute) {
		return absolute
	}
	if p, err := execLookPath(name); err == nil {
		return p
	}
	return ""
}

// runArgv executes one fixed program with argument-vector only (ADR-0003:
// no shell, no string concatenation of inputs).
func runArgv(ctx context.Context, bin string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // bin from LookPath/well-known path, args are fixed
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func execLookPath(name string) (string, error) { return exec.LookPath(name) }
