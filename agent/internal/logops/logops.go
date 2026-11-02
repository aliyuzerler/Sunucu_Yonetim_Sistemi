// Package logops implements the Faz 13 read-only log-search adapter:
// journald queries through journalctl with a FIXED argv template. Security
// contract (ADR-0003, ADR-0017):
//   - The binary path is fixed (configurable ONLY for tests); arguments are
//     built from validated, bounded parameters — never from raw strings.
//   - Unit names pass the systemd-unit allowlist; priorities are 0..7;
//     timestamps must be RFC3339; the free-text filter is length-bounded
//     and applied AFTER redaction.
//   - Output is bounded (≤500 entries, ≤2 MiB stdout) and every message is
//     SECRET-REDACTED before it ever leaves the node.
package logops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OpLogSearch is the operation name (mirrors backend ops_faz13.go).
const OpLogSearch = "logs.search"

// Bounds (mirror CP validators).
const (
	DefaultLimit   = 200
	MaxLimit       = 500
	MaxContains    = 256
	MaxStdoutBytes = 2 << 20 // 2 MiB
	MaxMessageRun  = 4096    // per-message cap after single-line flattening
)

// Errors (typed; executor maps to output text).
var (
	ErrInvalidParams         = errors.New("logops: geçersiz arama parametreleri")
	ErrJournalctlUnavailable = errors.New("logops: journalctl kullanılamıyor")
	ErrPermission            = errors.New("logops: günlük okuma izni yok (systemd-journal grubu gerekir)")
	ErrTooLarge              = errors.New("logops: çıktı sınırı aşıldı")
)

// SearchParams mirrors the CP payload schema (strict decode both sides).
type SearchParams struct {
	Unit     string `json:"unit,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
	Contains string `json:"contains,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// unitRE mirrors the systemd unit allowlist (svcops/winops pattern).
var unitRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`)

// timeShapeRE is the cheap RFC3339 shape check.
var timeShapeRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)

// ValidateParams enforces the closed schema (agent-side mirror).
func ValidateParams(p SearchParams) error {
	if p.Unit != "" && !unitRE.MatchString(p.Unit) {
		return fmt.Errorf("%w: unit geçersiz", ErrInvalidParams)
	}
	if p.Priority < 0 || p.Priority > 7 {
		return fmt.Errorf("%w: priority 0..7 olmalı", ErrInvalidParams)
	}
	for name, ts := range map[string]string{"since": p.Since, "until": p.Until} {
		if ts == "" {
			continue
		}
		if !timeShapeRE.MatchString(ts) {
			return fmt.Errorf("%w: %s RFC3339 olmalı", ErrInvalidParams, name)
		}
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			return fmt.Errorf("%w: %s geçersiz zaman", ErrInvalidParams, name)
		}
	}
	if len(p.Contains) > MaxContains {
		return fmt.Errorf("%w: contains en fazla %d karakter", ErrInvalidParams, MaxContains)
	}
	if p.Contains != "" {
		for _, r := range p.Contains {
			if r < 0x20 && r != '\t' {
				return fmt.Errorf("%w: contains kontrol karakteri içeremez", ErrInvalidParams)
			}
		}
	}
	if p.Limit < 0 || p.Limit > MaxLimit {
		return fmt.Errorf("%w: limit 0..%d olmalı", ErrInvalidParams, MaxLimit)
	}
	return nil
}

// Entry is one bounded, redacted journal line.
type Entry struct {
	Timestamp    string `json:"timestamp"`            // RFC3339 (source: __REALTIME_TIMESTAMP)
	Severity     int    `json:"severity"`             // 0..7 (journald PRIORITY)
	SeverityName string `json:"severity_name"`        // emerg..debug
	Unit         string `json:"unit,omitempty"`       // _SYSTEMD_UNIT
	Identifier   string `json:"identifier,omitempty"` // SYSLOG_IDENTIFIER
	Message      string `json:"message"`              // redacted, single-line, ≤4 KiB
}

// Runner executes journalctl with a fixed argv template. JournalctlPath is
// overridable ONLY for tests (production default: "journalctl" from PATH).
type Runner struct {
	JournalctlPath string
}

// journalFields extracted from `journalctl --output=json` records.
type journalRecord struct {
	Realtime  string   `json:"__REALTIME_TIMESTAMP"`
	Priority  string   `json:"PRIORITY"`
	Message   []string `json:"MESSAGE"` // MESSAGE can be array (repeated) or string
	Unit      string   `json:"_SYSTEMD_UNIT"`
	Ident     string   `json:"SYSLOG_IDENTIFIER"`
	MsgSingle string   `json:"__CURSOR"` // unused; keeps JSON parsing lenient
}

// rawRecord keeps MESSAGE raw (string OR array) — typed union.
type rawRecord struct {
	Realtime   string          `json:"__REALTIME_TIMESTAMP"`
	Priority   string          `json:"PRIORITY"`
	Unit       string          `json:"_SYSTEMD_UNIT"`
	Identifier string          `json:"SYSLOG_IDENTIFIER"`
	Message    json.RawMessage `json:"MESSAGE"`
}

// Search runs the query and returns redacted entries (newest first —
// journalctl returns reverse chronological with -n).
func (r *Runner) Search(ctx context.Context, p SearchParams) ([]Entry, error) {
	if err := ValidateParams(p); err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = DefaultLimit
	}

	bin := r.JournalctlPath
	if bin == "" {
		bin = "journalctl"
	}

	// FIXED argv template: only allowlisted, bounded values enter.
	argv := []string{
		"--quiet", "--no-pager", "--output=json",
		"-n", strconv.Itoa(limit),
	}
	if p.Unit != "" {
		argv = append(argv, "-u", p.Unit)
	}
	if p.Priority > 0 {
		argv = append(argv, "-p", strconv.Itoa(p.Priority))
	}
	if p.Since != "" {
		argv = append(argv, "--since", p.Since)
	}
	if p.Until != "" {
		argv = append(argv, "--until", p.Until)
	}

	cmd := exec.CommandContext(ctx, bin, argv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("logops: stdout: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrJournalctlUnavailable, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, MaxStdoutBytes+1))
	if readErr == nil && len(data) > MaxStdoutBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, ErrTooLarge
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("logops: okuma: %w", readErr)
	}
	if waitErr != nil {
		msg := stderr.String()
		switch {
		case strings.Contains(msg, "Access denied"),
			strings.Contains(msg, "Permission denied"),
			strings.Contains(msg, "No journal files"):
			return nil, ErrPermission
		default:
			return nil, fmt.Errorf("%w: journalctl hata: %s", ErrJournalctlUnavailable, firstLine(msg))
		}
	}
	return parseAndRedact(data, p.Contains)
}

// parseAndRedact decodes the journalctl JSON array, converts each record to
// a bounded Entry and applies secret redaction BEFORE the contains filter
// (so a filter cannot be used to probe for secrets: redaction is strict).
func parseAndRedact(data []byte, contains string) ([]Entry, error) {
	var raws []rawRecord
	if err := json.Unmarshal(data, &raws); err != nil {
		// Empty output (no matches) is a valid JSON "[]" — anything else
		// non-parseable is an honest parse failure.
		return nil, fmt.Errorf("logops: journalctl çıktısı çözümlenemedi: %w", err)
	}
	out := make([]Entry, 0, len(raws))
	for _, rr := range raws {
		e := Entry{
			Severity:     6, // info default (journal priority 6)
			SeverityName: "info",
		}
		if rr.Realtime != "" {
			if micros, err := strconv.ParseInt(rr.Realtime, 10, 64); err == nil {
				e.Timestamp = time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
			}
		}
		if rr.Priority != "" {
			if pri, err := strconv.Atoi(rr.Priority); err == nil && pri >= 0 && pri <= 7 {
				e.Severity = pri
				e.SeverityName = priorityName(pri)
			}
		}
		e.Unit = truncate(rr.Unit, 128)
		e.Identifier = truncate(rr.Identifier, 128)
		msg := messageText(rr.Message)
		// SECRET REDACTION — before any user-provided filtering.
		e.Message = truncate(RedactLine(msg), MaxMessageRun)
		if contains != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(contains)) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// messageText flattens the MESSAGE field (string or array-of-strings) into
// one line without control characters.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return flatten(s)
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		// journald splits long lines into byte chunks — join WITHOUT adding
		// separators (chunks already carry their spacing).
		return flatten(strings.Join(arr, ""))
	}
	return ""
}

// flatten removes newlines (single-line entries keep the wire shape sane).
func flatten(s string) string {
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	return strings.TrimSpace(r.Replace(s))
}

// truncate bounds a string at n bytes (rune-safe).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for i := n; i > n-4 && i > 0; i-- {
		if (s[i] & 0xC0) != 0x80 {
			return s[:i] + "…"
		}
	}
	return s[:n]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 200)
}

// priorityName maps journald priorities 0..7 to stable names.
func priorityName(p int) string {
	names := []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
	if p < 0 || p >= len(names) {
		return "unknown"
	}
	return names[p]
}
