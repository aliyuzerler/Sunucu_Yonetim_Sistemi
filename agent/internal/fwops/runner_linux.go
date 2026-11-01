//go:build linux

package fwops

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// maxRulesOutput bounds ruleset stdout (64 KiB — rule listings can be huge
// on real hosts; the listing is a survey, not an export).
const maxRulesOutput = 64 << 10

// Runner detects the active firewall backend and lists its rules. Tool
// paths are injectable for hermetic tests; zero value looks up on PATH.
type Runner struct {
	FirewallCmd  string
	Ufw          string
	Nft          string
	IptablesSave string
	Timeout      time.Duration
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 20 * time.Second
}

// Probe detects the active backend with honest differences: firewalld
// (running) → ufw (active) → nftables (ruleset readable) → iptables
// (save readable) → none. Read-only throughout.
func (r *Runner) Probe(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	if fc := r.lookPath("firewalld", r.FirewallCmd, "firewall-cmd"); fc != "" {
		if out, err := runArgv(ctx, fc, "--state"); err == nil && strings.TrimSpace(out) == "running" {
			return Status{Backend: BackendFirewalld, Active: true, Version: toolVersion(ctx, fc, "-V"), Reason: "firewalld çalışıyor"}
		}
		// installed but not running → still report honestly
		return Status{Backend: BackendFirewalld, Active: false, Reason: "firewalld kurulu ama çalışmıyor"}
	}
	if ufw := r.lookPath("ufw", r.Ufw, "ufw"); ufw != "" {
		if out, err := runArgv(ctx, ufw, "status"); err == nil {
			if ParseUfwActive(out) {
				return Status{Backend: BackendUfw, Active: true, Reason: "ufw aktif"}
			}
			return Status{Backend: BackendUfw, Active: false, Reason: "ufw kurulu ama inaktif"}
		}
	}
	if nft := r.lookPath("nft", r.Nft, "nft"); nft != "" {
		if _, err := runArgv(ctx, nft, "list", "ruleset"); err == nil {
			return Status{Backend: BackendNft, Active: true, Reason: "nftables ruleset okunabilir"}
		}
	}
	if ip := r.lookPath("iptables", r.IptablesSave, "iptables-save"); ip != "" {
		if _, err := runArgv(ctx, ip); err == nil {
			return Status{Backend: BackendIptables, Active: true, Reason: "iptables-save okunabilir"}
		}
	}
	return Status{Backend: BackendNone, Active: false, Reason: "tanınan firewall aracı yok"}
}

// Rules returns one bounded ruleset from the ACTIVE backend (read-only).
func (r *Runner) Rules(ctx context.Context, limit int) (Ruleset, error) {
	if limit < 1 || limit > 500 {
		return Ruleset{}, ErrInvalidPreview
	}
	st := r.Probe(ctx)
	if st.Backend == BackendNone {
		return Ruleset{Backend: st.Backend, Active: false, Reason: st.Reason}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	var out string
	var err error
	switch st.Backend {
	case BackendFirewalld:
		out, err = r.firewalldRules(ctx)
	case BackendUfw:
		out, err = runArgv(ctx, r.lookPath("ufw", r.Ufw, "ufw"), "status", "verbose")
	case BackendNft:
		out, err = runArgv(ctx, r.lookPath("nft", r.Nft, "nft"), "list", "ruleset")
	case BackendIptables:
		out, err = runArgv(ctx, r.lookPath("iptables", r.IptablesSave, "iptables-save"))
	}
	if err != nil {
		return Ruleset{Backend: st.Backend, Active: st.Active, Reason: st.Reason}, err
	}
	lines := boundedLines(out)
	lines, truncated := ClampLines(lines, limit)
	return Ruleset{Backend: st.Backend, Active: st.Active, Lines: lines, Truncated: truncated}, nil
}

// firewalldRules lists every active zone (bounded) read-only.
func (r *Runner) firewalldRules(ctx context.Context) (string, error) {
	fc := r.lookPath("firewalld", r.FirewallCmd, "firewall-cmd")
	zones, err := runArgv(ctx, fc, "--get-active-zones")
	if err != nil {
		return "", err
	}
	names := ParseActiveZones(zones)
	if len(names) == 0 {
		names = []string{"public"}
	}
	var buf bytes.Buffer
	for i, z := range names {
		if i > 0 {
			buf.WriteString("\n")
		}
		out, err := runArgv(ctx, fc, "--zone="+z, "--list-all")
		if err != nil {
			buf.WriteString("zone " + z + ": okunamadı")
			continue
		}
		buf.WriteString("[" + z + "]\n")
		buf.WriteString(out)
	}
	return buf.String(), nil
}

func (r *Runner) lookPath(pkg, injected, fallback string) string {
	if injected != "" {
		return injected
	}
	if p, err := exec.LookPath(fallback); err == nil {
		return p
	}
	return ""
}

func toolVersion(ctx context.Context, bin string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runArgv(ctx, bin, args...)
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) >= 2 {
		return fields[len(fields)-1]
	}
	return ""
}

// boundedLines splits, trims empties, bounds to 400 lines.
func boundedLines(out string) []string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			continue
		}
		if len(l) > 500 {
			l = l[:500]
		}
		lines = append(lines, l)
	}
	if len(lines) > 400 {
		lines = lines[:400]
	}
	return lines
}

// runArgv executes one fixed program with an argument vector (no shell).
func runArgv(ctx context.Context, bin string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + allowlisted/enum args
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}
