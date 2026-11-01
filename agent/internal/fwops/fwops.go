// Package fwops implements the Faz 06 firewall adapter. Faz 06 scope:
//   - backend DETECTION with honest differences (firewalld | ufw | nftables
//     | iptables | none) — criterion "firewall backend differences must be
//     recognized correctly";
//   - READ-ONLY rule listing per backend;
//   - RULE-CHANGE PREVIEW ONLY: the preview operation renders the exact
//     argv commands a mutation WOULD run and returns them as data. Nothing
//     is ever executed (criterion "automatic rule changes require a safe
//     preview" — implemented as the stronger preview-only policy, ADR-0010).
//
// Security (ADR-0003): every external call uses a FIXED argv template; the
// only caller-controlled strings are the zone name (strict allowlist) and
// bounded ints (port). XPath/shell composition is impossible by design.
package fwops

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Operation names (mirror backend/internal/tasks ops_faz06.go).
const (
	OpLinuxFirewallStatus      = "linux.firewall.status"
	OpLinuxFirewallRulesList   = "linux.firewall.rules.list"
	OpLinuxFirewallRulePreview = "linux.firewall.rule.preview"
)

// Firewall backends (closed set; "none" = no recognized tool).
const (
	BackendFirewalld = "firewalld"
	BackendUfw       = "ufw"
	BackendNft       = "nftables"
	BackendIptables  = "iptables"
	BackendNone      = "none"
)

// Stable errors.
var (
	ErrPreviewUnsupported = errors.New("bu backend için kural önizlemesi yok (firewalld|ufw desteklenir)")
	ErrInvalidPreview     = errors.New("geçersiz kural önizleme parametreleri")
)

// zoneRE: firewalld zone names (closed shape; user-controlled value in an
// otherwise fixed argv — strict allowlist).
var zoneRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,32}$`)

// ValidZone reports whether the zone name is allowed (empty = default zone).
func ValidZone(z string) bool { return zoneRE.MatchString(z) }

// Action values for the preview payload.
const (
	ActionAllow  = "allow"
	ActionRevoke = "revoke"
)

// Status is the detected firewall state (typed).
type Status struct {
	Backend string `json:"backend"` // firewalld|ufw|nftables|iptables|none
	Active  bool   `json:"active"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"` // why inactive/none (honest reporting)
}

// Ruleset is one bounded rules listing.
type Ruleset struct {
	Backend   string   `json:"backend"`
	Active    bool     `json:"active"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
	Reason    string   `json:"reason,omitempty"` // honest state note (inactive/none)
}

// PreviewParams is the typed payload of linux.firewall.rule.preview.
type PreviewParams struct {
	Zone     string `json:"zone,omitempty"` // "" = backend default zone
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // tcp | udp
	Action   string `json:"action"`   // allow | revoke
}

// ValidatePreviewParams enforces the closed enums + bounded ints.
func ValidatePreviewParams(p PreviewParams) error {
	if !ValidZone(p.Zone) {
		return fmt.Errorf("%w: zone biçimi [A-Za-z0-9_.-]{0,32} olmalı", ErrInvalidPreview)
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("%w: port 1..65535 olmalı", ErrInvalidPreview)
	}
	if p.Protocol != "tcp" && p.Protocol != "udp" {
		return fmt.Errorf("%w: protocol tcp|udp olmalı", ErrInvalidPreview)
	}
	if p.Action != ActionAllow && p.Action != ActionRevoke {
		return fmt.Errorf("%w: action allow|revoke olmalı", ErrInvalidPreview)
	}
	return nil
}

// Preview is the SAFE result of the preview operation: the exact commands
// that WOULD be executed, returned as data. Executed is always false — the
// preview never runs anything (ADR-0010).
type Preview struct {
	Backend  string   `json:"backend"`
	Commands []string `json:"commands"` // argv joined for display, one per command
	Executed bool     `json:"executed"` // always false; documented invariant
	Note     string   `json:"note"`     // operator guidance
}

// RenderPreview renders the per-backend commands (pure; differences between
// backends are explicit here — criterion "backend differences recognized").
// nftables/iptables previews are intentionally rejected: raw rule syntax is
// host-specific and rendering it would imply safety we cannot prove; those
// backends are report-only in Faz 06 (documented in linux-support.md).
func RenderPreview(backend string, p PreviewParams) (Preview, error) {
	if err := ValidatePreviewParams(p); err != nil {
		return Preview{}, err
	}
	portProto := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
	switch backend {
	case BackendFirewalld:
		z := p.Zone
		if z == "" {
			z = "public"
		}
		var cmds []string
		if p.Action == ActionAllow {
			cmds = []string{
				fmt.Sprintf("firewall-cmd --permanent --zone=%s --add-port=%s", z, portProto),
				"firewall-cmd --reload",
			}
		} else {
			cmds = []string{
				fmt.Sprintf("firewall-cmd --permanent --zone=%s --remove-port=%s", z, portProto),
				"firewall-cmd --reload",
			}
		}
		return Preview{
			Backend:  backend,
			Commands: cmds,
			Executed: false,
			Note:     "ÖNİZLEME — hiçbir komut çalıştırılmadı. Uygulama operatör onayıyla ayrı adımda yapılır (ADR-0010).",
		}, nil
	case BackendUfw:
		var cmds []string
		if p.Action == ActionAllow {
			cmds = []string{fmt.Sprintf("ufw allow %s", portProto)}
		} else {
			cmds = []string{fmt.Sprintf("ufw delete allow %s", portProto)}
		}
		return Preview{
			Backend:  backend,
			Commands: cmds,
			Executed: false,
			Note:     "ÖNİZLEME — hiçbir komut çalıştırılmadı. Uygulama operatör onayıyla ayrı adımda yapılır (ADR-0010).",
		}, nil
	default:
		return Preview{}, fmt.Errorf("%w: backend=%s", ErrPreviewUnsupported, backend)
	}
}

// ParseUfwActive reports whether `ufw status` output says active.
func ParseUfwActive(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Status:") {
			return strings.Contains(strings.ToLower(l), "active") &&
				!strings.Contains(strings.ToLower(l), "inactive")
		}
	}
	return false
}

// ParseActiveZones extracts zone names from `firewall-cmd --get-active-zones`
// output ("ZONE\n  interfaces: …" shape); only shape-valid zones are kept.
func ParseActiveZones(out string) []string {
	var zones []string
	for _, line := range strings.Split(out, "\n") {
		z := strings.TrimSpace(line)
		if z == "" || strings.Contains(z, ":") || strings.Contains(z, " ") {
			continue // zone header lines are bare tokens; skip interface lines
		}
		if !ValidZone(z) {
			continue
		}
		zones = append(zones, z)
	}
	if len(zones) > 16 {
		zones = zones[:16]
	}
	return zones
}

// ClampLines bounds a rule listing (line cap for log safety).
func ClampLines(lines []string, limit int) ([]string, bool) {
	truncated := false
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}
	return lines, truncated
}
