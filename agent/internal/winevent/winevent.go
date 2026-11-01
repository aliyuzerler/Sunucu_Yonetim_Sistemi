//go:build windows

// Package winevent implements the Faz 05 Windows Event Log reader.
//
// Security (ADR-0003, ADR-0009): wevtutil.exe is executed with an ARGUMENT
// VECTOR built from a FIXED template. The channel is a closed enum, level is
// a bounded int and NO XPath/query text is ever composed from untrusted
// input. Output is parsed with encoding/xml into bounded structs. There is
// NO PowerShell anywhere.
package winevent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// Reader reads event-log channels via wevtutil. Zero value looks wevtutil up
// on PATH falling back to System32; Timeout (zero = 15s) bounds each query.
type Reader struct {
	Wevtutil string
	Timeout  time.Duration
}

func (r *Reader) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 15 * time.Second
}

func (r *Reader) wevtutil() string {
	if r.Wevtutil != "" {
		return r.Wevtutil
	}
	if p, err := exec.LookPath("wevtutil"); err == nil {
		return p
	}
	return `C:\Windows\System32\wevtutil.exe`
}

// Read returns the newest events of a channel (newest first), optionally
// filtered by level, bounded by count (winops-validated upstream too).
//
// Implementation note: level filtering is done CLIENT-SIDE on purpose —
// wevtutil level filtering would need an XPath argument; fetching with
// /rd:true (reverse direction) and filtering here keeps the command template
// 100% fixed. To make a strict-level filter of N events likely, we overfetch
// (count*3, capped 600) and cut to count.
func (r *Reader) Read(ctx context.Context, p winops.EventlogParams) ([]winops.Event, error) {
	if err := winops.ValidateEventlogParams(p); err != nil {
		return nil, err
	}
	count := p.Count
	if count == 0 {
		count = 50
	}
	fetch := count * 3
	if fetch > 600 {
		fetch = 600
	}
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	// FIXED argv template: channel is enum-validated, counts are ints.
	args := []string{
		"query-events", p.Channel,
		"/count:" + strconv.Itoa(fetch),
		"/rd:true",
		"/format:xml",
	}
	out, err := runArgv(ctx, r.wevtutil(), args...)
	if err != nil {
		return nil, fmt.Errorf("wevtutil başarısız: %v", err)
	}
	events := winops.ParseWevtutilXML(out)
	filtered := make([]winops.Event, 0, count)
	for _, e := range events {
		if p.Level != 0 && e.Level != p.Level {
			continue
		}
		filtered = append(filtered, e)
		if len(filtered) >= count {
			break
		}
	}
	return filtered, nil
}

// ProbeAvailable reports whether the event-log capability is usable right
// now (cheap System-channel probe). Used for capability reporting.
func (r *Reader) ProbeAvailable(ctx context.Context) (bool, string) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := r.Read(probeCtx, winops.EventlogParams{Channel: "System", Count: 1})
	if err != nil {
		return false, "eventlog_unavailable"
	}
	return true, ""
}

// runArgv executes one fixed program with an argument vector (no shell).
func runArgv(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + enum/validated args
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.WaitDelay = 2 * time.Second
	err := c.Run()
	if err != nil {
		msg := stderr.String()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("%v: %s", err, msg)
	}
	return stdout.Bytes(), nil
}
