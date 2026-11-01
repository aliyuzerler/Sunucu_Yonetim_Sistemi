// Package winops holds the CROSS-PLATFORM pieces of the Faz 05 Windows
// adapters: typed payload schemas, allowlist validators and pure parsers.
// Nothing in this package may import OS-specific packages, so unit tests run
// on any GOOS (ADR-0009). Windows-only syscall code lives in winscm/winevent
// and broker/auth_windows.go.
//
// Security rules (ADR-0003, ADR-0009):
//   - Service names are strictly allowlisted BEFORE any process or SCM call.
//   - Event Log reads use wevtutil argv with a FIXED argument template; the
//     channel comes from a closed enum and level/count from bounded ints, so
//     no query text is ever composed from untrusted input.
//   - There is intentionally NO PowerShell execution surface anywhere.
package winops

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Operation names (must mirror backend/internal/tasks ops_windows.go).
const (
	OpWinServiceList    = "windows.scm.service.list"
	OpWinServiceStatus  = "windows.scm.service.status"
	OpWinServiceStart   = "windows.scm.service.start"
	OpWinServiceStop    = "windows.scm.service.stop"
	OpWinServiceRestart = "windows.scm.service.restart"
	OpWinEventlogRead   = "windows.eventlog.read"
)

// scmNameRE: SCM service KEY names (not display names). First char must be
// alphanumeric (rejects leading "-" → no argument injection); body allows
// letters/digits/space/dot/underscore/dash; 80 chars bound (practical SCM
// key names ≤ 80); path separators, shell metacharacters and quotes are
// excluded by the class.
var scmNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$`)

// ValidateServiceName enforces the SCM service-name allowlist.
func ValidateServiceName(name string) error {
	if !scmNameRE.MatchString(name) {
		return ErrInvalidService
	}
	// Trailing dot/space confuses Win32 path normalization; reject explicitly.
	last := name[len(name)-1]
	if last == '.' || last == ' ' {
		return ErrInvalidService
	}
	return nil
}

// ErrInvalidService is the stable validation error for service names.
var ErrInvalidService = errors.New("geçersiz servis adı")

// linuxUnitRE mirrors the systemd unit allowlist (svcops): first char
// alphanumeric (no flag injection), template units allowed, no path
// separators or shell metacharacters, mandatory .service suffix,
// UNIT_NAME_MAX=256 bounded. Lives HERE so linux unit validation is
// testable on every GOOS (svcops itself is unix-only).
var linuxUnitRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`)

// ErrInvalidLinuxUnit is the stable validation error for systemd units.
var ErrInvalidLinuxUnit = errors.New("geçersiz servis birimi adı")

// ValidateLinuxUnit enforces the systemd unit-name allowlist.
func ValidateLinuxUnit(unit string) error {
	if !linuxUnitRE.MatchString(unit) {
		return ErrInvalidLinuxUnit
	}
	return nil
}

// ServiceParams is the schema for name-targeted SCM params.
type ServiceParams struct {
	Service string `json:"service"`
}

// ServiceInfo is the typed status of one Windows service (SCM adapter
// output; cross-platform so the broker and backend mirrors share it).
type ServiceInfo struct {
	Name     string `json:"name"`      // SCM key name
	Status   string `json:"status"`    // stopped|start_pending|stop_pending|running|continue_pending|pause_pending|paused|unknown
	StartUp  string `json:"start_up"`  // boot|system|auto|demand|disabled|unknown
	ExitCode int    `json:"exit_code"` // win32 exit code when stopped
	PID      uint32 `json:"pid"`       // 0 when stopped
}

// ListParams is the schema for list params (empty object or with limit).
type ListParams struct {
	Limit int `json:"limit,omitempty"`
}

// ValidateLimit bounds a list limit to 1..500 (0 → default 200).
func ValidateLimit(limit int) (int, error) {
	if limit == 0 {
		return 200, nil
	}
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("limit 1..500 olmalı")
	}
	return limit, nil
}

// ---- Event Log (windows.eventlog.read) ----

// EventLog channels the agent will read. Closed enum: Security is
// deliberately EXCLUDED (requires admin; the agent runs unprivileged and
// Security logs are security-sensitive — see ADR-0009 / windows-support.md).
var EventChannels = map[string]bool{
	"Application": true,
	"System":      true,
	"Setup":       true,
}

// EventLog levels (standard Windows values).
const (
	LevelCritical    = 1
	LevelError       = 2
	LevelWarning     = 3
	LevelInformation = 4
	LevelVerbose     = 5
)

// LevelName maps a numeric level to a stable identifier.
func LevelName(l int) string {
	switch l {
	case LevelCritical:
		return "critical"
	case LevelError:
		return "error"
	case LevelWarning:
		return "warning"
	case LevelInformation:
		return "information"
	case LevelVerbose:
		return "verbose"
	default:
		return "unknown"
	}
}

// EventlogParams is the typed schema for windows.eventlog.read. Level 0 = no
// filter; Count 1..200.
type EventlogParams struct {
	Channel string `json:"channel"`
	Level   int    `json:"level,omitempty"`
	Count   int    `json:"count,omitempty"`
}

// ValidateEventlogParams checks the closed channel enum and bounded ints.
func ValidateEventlogParams(p EventlogParams) error {
	if !EventChannels[p.Channel] {
		return fmt.Errorf("channel Application|System|Setup olmalı")
	}
	if p.Level < 0 || p.Level > 5 {
		return fmt.Errorf("level 0..5 olmalı")
	}
	if p.Count < 0 || p.Count > 200 {
		return fmt.Errorf("count 0..200 olmalı")
	}
	if p.Count == 0 {
		p.Count = 50
	}
	return nil
}

// Event is one bounded, rendered event-log record.
type Event struct {
	RecordID    uint64 `json:"record_id"`
	TimeCreated string `json:"time_created"` // RFC3339 from SystemTime
	Level       int    `json:"level"`
	LevelName   string `json:"level_name"`
	EventID     int    `json:"event_id"`
	Provider    string `json:"provider"`
	Channel     string `json:"channel"`
	Computer    string `json:"computer"`
	Message     string `json:"message,omitempty"` // truncated, single line
}

// ---- wevtutil XML parsing (pure; fixture-tested on every GOOS) ----

type xmlEvents struct {
	Events []xmlEvent `xml:"Event"`
}

type xmlEvent struct {
	System        xmlSystem     `xml:"System"`
	RenderingInfo *xmlRendering `xml:"RenderingInfo"`
}

type xmlSystem struct {
	Provider    xmlProvider `xml:"Provider"`
	EventID     xmlEventID  `xml:"EventID"`
	Level       int         `xml:"Level"`
	TimeCreated struct {
		SystemTime string `xml:"SystemTime,attr"`
	} `xml:"TimeCreated"`
	EventRecordID uint64 `xml:"EventRecordID"`
	Channel       string `xml:"Channel"`
	Computer      string `xml:"Computer"`
}

type xmlProvider struct {
	Name string `xml:"Name,attr"`
}

type xmlEventID struct {
	Value  int `xml:",chardata"`
	Qualif int `xml:"Qualifiers,attr"`
}

type xmlRendering struct {
	Message string `xml:"Message"`
}

// ParseWevtutilXML parses `wevtutil query-events … /format:xml` output into
// bounded Events. Message is collapsed to one line and truncated to 4000
// bytes. Malformed documents yield nil; unknown fields never fail the read.
func ParseWevtutilXML(data []byte) []Event {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // BOM
	var wrapper xmlEvents
	if err := xml.Unmarshal(data, &wrapper); err != nil {
		return nil
	}
	out := make([]Event, 0, len(wrapper.Events))
	for _, e := range wrapper.Events {
		msg := ""
		if e.RenderingInfo != nil {
			msg = e.RenderingInfo.Message
		}
		ev := Event{
			RecordID:    e.System.EventRecordID,
			TimeCreated: e.System.TimeCreated.SystemTime,
			Level:       e.System.Level,
			LevelName:   LevelName(e.System.Level),
			EventID:     e.System.EventID.Value,
			Provider:    e.System.Provider.Name,
			Channel:     e.System.Channel,
			Computer:    e.System.Computer,
			Message:     oneLine(msg),
		}
		out = append(out, ev)
		if len(out) >= 200 {
			break // bound
		}
	}
	return out
}

// oneLine collapses whitespace runs and truncates (log safety).
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 4000 {
		s = s[:4000]
	}
	return s
}
