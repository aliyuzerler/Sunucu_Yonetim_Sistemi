// Package svcops implements the Faz 04 Linux systemd service adapter.
//
// Security rules (docs/architecture/agent-architecture.md, ADR-0003):
//   - Unit names are strictly validated (allowlist regex); anything outside
//     the allowlist is rejected BEFORE any process is spawned.
//   - systemctl is executed with an argument vector ONLY. Inputs are never
//     merged into shell strings and no shell is ever spawned.
//   - Output is bounded; execution is time-limited.
package svcops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Operation error categories (stable API contract).
var (
	ErrInvalidUnit   = errors.New("geçersiz servis birimi adı")
	ErrSystemdAbsent = errors.New("systemd kullanılamıyor")
	ErrTimeout       = errors.New("işlem zaman aşımına uğradı")
)

// unitNameRE: systemd service unit names. First char must be alphanumeric
// (rejects leading "-" → no flag injection into systemctl); the body allows
// template units (user@1000.service, systemd-fsck@dev-sda1.service); path
// separators, shell metacharacters and % specifiers are excluded by the class;
// UNIT_NAME_MAX is 256.
var unitNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`)

// ValidateUnit enforces the unit-name allowlist.
func ValidateUnit(unit string) error {
	if !unitNameRE.MatchString(unit) {
		return ErrInvalidUnit
	}
	return nil
}

// UnitInfo is the typed status of one systemd service unit.
type UnitInfo struct {
	ID          string `json:"id"`           // unit name, e.g. nginx.service
	LoadState   string `json:"load_state"`   // loaded | not-found | masked…
	ActiveState string `json:"active_state"` // active | inactive | failed…
	SubState    string `json:"sub_state"`    // running | dead | exited…
	Description string `json:"description"`
}

// Manager runs systemctl operations. Systemctl path is resolved once; zero
// value looks systemctl up on the PATH, falling back to /usr/bin/systemctl.
// Timeout (zero = 60s default) bounds each verb/list/status execution.
type Manager struct {
	Systemctl string
	Timeout   time.Duration
}

// execTimeout returns the configured (or default) per-operation timeout.
func (m *Manager) execTimeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 60 * time.Second
}

// SystemctlPath resolves the systemctl binary.
func (m *Manager) systemctl() string {
	if m.Systemctl != "" {
		return m.Systemctl
	}
	if p, err := exec.LookPath("systemctl"); err == nil {
		return p
	}
	return "/usr/bin/systemctl"
}

// List returns all currently known service units. Read-only.
func (m *Manager) List(ctx context.Context) ([]UnitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, m.execTimeout())
	defer cancel()
	out, err := runArgv(ctx, m.systemctl(),
		"list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSystemdAbsent, boundOutput([]byte(out), err))
	}
	return parseListUnits(out), nil
}

// Status returns one unit's typed properties. Read-only.
func (m *Manager) Status(ctx context.Context, unit string) (UnitInfo, error) {
	if err := ValidateUnit(unit); err != nil {
		return UnitInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, m.execTimeout())
	defer cancel()
	out, err := runArgv(ctx, m.systemctl(), "show", unit,
		"--property=Id,LoadState,ActiveState,SubState,Description", "--no-pager")
	if err != nil {
		return UnitInfo{}, fmt.Errorf("show başarısız: %s", boundOutput([]byte(out), err))
	}
	u := UnitInfo{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "Id":
			u.ID = v
		case "LoadState":
			u.LoadState = v
		case "ActiveState":
			u.ActiveState = v
		case "SubState":
			u.SubState = v
		case "Description":
			u.Description = v
		}
	}
	return u, nil
}

// Start / Stop / Restart run the corresponding systemctl verb. These are
// privileged actions; the CALLER (broker) is responsible for the allowlist,
// identity check and audit. Args are separate argv elements — never a shell.
func (m *Manager) Start(ctx context.Context, unit string) error { return m.verb(ctx, "start", unit) }
func (m *Manager) Stop(ctx context.Context, unit string) error  { return m.verb(ctx, "stop", unit) }
func (m *Manager) Restart(ctx context.Context, unit string) error {
	return m.verb(ctx, "restart", unit)
}

func (m *Manager) verb(ctx context.Context, action, unit string) error {
	if err := ValidateUnit(unit); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, m.execTimeout())
	defer cancel()
	out, err := runArgv(ctx, m.systemctl(), action, unit)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w (%s %s)", ErrTimeout, action, unit)
		}
		return fmt.Errorf("%s başarısız: %s", action, boundOutput([]byte(out), err))
	}
	return nil
}

// parseListUnits parses `list-units --no-legend --plain` rows:
// UNIT LOAD ACTIVE SUB DESCRIPTION (description may contain spaces).
func parseListUnits(out string) []UnitInfo {
	var units []UnitInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 5)
		if len(fields) < 4 {
			continue
		}
		u := UnitInfo{ID: fields[0], LoadState: fields[1], ActiveState: fields[2], SubState: fields[3]}
		if len(fields) == 5 {
			u.Description = strings.TrimSpace(fields[4])
		}
		units = append(units, u)
		if len(units) >= 500 {
			break // bound
		}
	}
	return units
}

// boundOutput clamps combined output for error messages (log safety).
func boundOutput(out []byte, err error) string {
	var buf bytes.Buffer
	if len(out) > 0 {
		buf.Write(out)
	}
	if err != nil {
		if buf.Len() > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString(err.Error())
	}
	s := strings.TrimSpace(buf.String())
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// runArgv executes one fixed program with an argument vector (no shell).
func runArgv(ctx context.Context, bin string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + validated argv
	var stdout bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stdout
	c.WaitDelay = 2 * time.Second
	err := c.Run()
	return stdout.String(), err
}
