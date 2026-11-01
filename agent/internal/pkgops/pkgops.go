// Package pkgops implements the Faz 06 read-only package inventory adapter.
// It is intentionally INVENTORY-ONLY: there is NO install/remove/update
// surface anywhere (documented in docs/architecture/linux-support.md —
// package mutation is a later phase with its own approval design).
//
// Security (ADR-0003): dpkg-query/rpm run with a FIXED argument template;
// the only caller-controlled value is the list limit (bounded int parsed by
// the broker/backend validators). Output is parsed into typed structs with
// pure functions so unit tests run on every GOOS.
package pkgops

import (
	"errors"
	"strings"
)

// OpLinuxPkgList is the read-only package inventory operation.
const OpLinuxPkgList = "linux.pkg.list"

// ErrPkgUnavailable reports that no supported package tool was found.
var ErrPkgUnavailable = errors.New("paket aracı bulunamadı (dpkg-query/rpm)")

// ErrLimit is the stable validation error for the list limit.
var ErrLimit = errors.New("limit 1..500 olmalı")

// PackageInfo is one installed package (typed, bounded).
type PackageInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch,omitempty"`
	Source  string `json:"source"` // rpm | dpkg (which tool reported it)
}

// ValidateLimit bounds the inventory limit (0 → default 200).
func ValidateLimit(limit int) (int, error) {
	if limit == 0 {
		return 200, nil
	}
	if limit < 1 || limit > 500 {
		return 0, ErrLimit
	}
	return limit, nil
}

// Clamp clamps a parsed list to limit.
func Clamp(in []PackageInfo, limit int) []PackageInfo {
	if len(in) > limit {
		return in[:limit]
	}
	return in
}

// ParseRpmQA parses `rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n'`
// output. Malformed lines are skipped (never fail the read); empty arch is
// tolerated. Names are echo-guarded: anything with control characters is
// dropped.
func ParseRpmQA(out string) []PackageInfo {
	return parseTsv(out, "rpm")
}

// ParseDpkgQuery parses
// `dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n'` output.
func ParseDpkgQuery(out string) []PackageInfo {
	return parseTsv(out, "dpkg")
}

// parseTsv is the shared line parser (tab-separated, ≤3 columns).
func parseTsv(out, source string) []PackageInfo {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	lines := strings.Split(out, "\n")
	parsed := make([]PackageInfo, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 2 {
			continue // header/noise lines: skip quietly
		}
		name := strings.TrimSpace(parts[0])
		version := strings.TrimSpace(parts[1])
		arch := ""
		if len(parts) >= 3 {
			arch = strings.TrimSpace(parts[2])
		}
		if name == "" || version == "" || hasControl(name) || hasControl(version) {
			continue // never echo control characters
		}
		if len(name) > 200 || len(version) > 200 {
			continue // bound per-field (log safety)
		}
		parsed = append(parsed, PackageInfo{Name: name, Version: version, Arch: arch, Source: source})
	}
	return parsed
}

// hasControl rejects ASCII control characters (0x00-0x1F, 0x7F) anywhere in
// a field.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7F {
			return true
		}
	}
	return false
}
