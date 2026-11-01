//go:build linux

package pkgops

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// maxToolOutput bounds dpkg-query/rpm stdout (log/memory safety).
const maxToolOutput = 1 << 20 // 1 MiB

// Runner executes the read-only inventory against the real host. Tool paths
// are injectable for hermetic tests; zero value looks both up on PATH.
type Runner struct {
	DpkgQuery string // path to dpkg-query ("" → look up)
	Rpm       string // path to rpm ("" → look up)
	Timeout   time.Duration
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 30 * time.Second
}

// tool holds one resolved backend.
type tool struct {
	name string
	bin  string
	args []string
}

// pick chooses the highest-level available tool (dpkg-query for the deb
// family, rpm for the rhel family).
func (r *Runner) pick() (tool, bool) {
	if t, ok := r.resolve("dpkg-query", "dpkg-query", []string{"-W", "-f=${Package}\t${Version}\t${Architecture}\n"}); ok {
		return t, true
	}
	if t, ok := r.resolve("rpm", "rpm", []string{"-qa", "--qf=%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n"}); ok {
		return t, true
	}
	return tool{}, false
}

func (r *Runner) resolve(name, fallback string, args []string) (tool, bool) {
	bin := ""
	switch name {
	case "dpkg-query":
		bin = r.DpkgQuery
	case "rpm":
		bin = r.Rpm
	}
	if bin == "" {
		if p, err := execLookPath(fallback); err == nil {
			bin = p
		}
	}
	if bin == "" {
		return tool{}, false
	}
	return tool{name: name, bin: bin, args: args}, true
}

// List returns the installed packages (bounded to limit).
func (r *Runner) List(ctx context.Context, limit int) ([]PackageInfo, error) {
	limit, err := ValidateLimit(limit)
	if err != nil {
		return nil, err
	}
	t, ok := r.pick()
	if !ok {
		return nil, ErrPkgUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	out, err := runArgv(ctx, t.bin, t.args...)
	if err != nil {
		return nil, err
	}
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput]
	}
	var parsed []PackageInfo
	if t.name == "rpm" {
		parsed = ParseRpmQA(string(bytes.TrimSpace(out)))
	} else {
		parsed = ParseDpkgQuery(string(bytes.TrimSpace(out)))
	}
	return Clamp(parsed, limit), nil
}

// runArgv executes one fixed program with an argument vector (no shell).
func runArgv(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // bin from LookPath/injected path, args are fixed
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		msg := stderr.String()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, err // stderr detail kept out of task results (log safety)
	}
	return stdout.Bytes(), nil
}

func execLookPath(name string) (string, error) { return exec.LookPath(name) }
