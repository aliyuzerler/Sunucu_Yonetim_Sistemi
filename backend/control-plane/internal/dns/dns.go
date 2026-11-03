// Package dns implements the Faz 09 DNS provider adapter layer.
//
// Architecture (ADR-0013, docs/architecture/dns-ssl.md):
//   - Providers live on the CONTROL PLANE (they need outbound internet +
//     credentials; nodes never hold provider tokens).
//   - The Provider interface is the only contract: capabilities, zone and
//     record CRUD. Implementations must be strictly typed — no shell, no
//     arbitrary command execution.
//   - The built-in "mock" provider is fully in-memory: it backs unit and
//     integration tests, e2e, and honest offline evaluation. Real provider
//     integrations (Cloudflare, Route53, ...) plug into the same interface
//     and are deliberately NOT in this phase (untestable without accounts;
//     "kullanıcı istemedikçe ücretli servis entegrasyonu" scope guard).
//   - Every mutation path goes through ValidateRecord + TTL bounds from the
//     provider's declared capabilities; the diff preview is computed BEFORE
//     apply and is part of the task result (audit trail via the task row).
package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Record types the mock provider (and the validation layer) supports.
// Closed enum: unknown types are refused, never guessed.
var RecordTypes = []string{"A", "AAAA", "CNAME", "TXT", "MX"}

// Capabilities is what a provider declares. The control plane USES the
// declaration (TTL bounds, DNS-01 support) instead of guessing.
type Capabilities struct {
	RecordTypes   []string `json:"record_types"`
	TTLMin        int      `json:"ttl_min"`
	TTLMax        int      `json:"ttl_max"`
	SupportsDNS01 bool     `json:"supports_dns01"`
	Wildcard      bool     `json:"wildcard"`
	Note          string   `json:"note,omitempty"`
}

// Record is one DNS resource record.
type Record struct {
	Name  string `json:"name"` // relative to zone; "@" = zone apex
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

// Change is one applied/previewed mutation.
type Change struct {
	Action string  `json:"action"` // create | update | delete
	Before *Record `json:"before,omitempty"`
	After  *Record `json:"after,omitempty"`
}

// Provider is the Faz 09 adapter contract. Implementations: mock (built-in).
type Provider interface {
	// Capabilities declares TTL bounds, record types, DNS-01 support.
	Capabilities() Capabilities
	// Zones lists hosted zones (absolute names, no trailing dot).
	Zones(ctx context.Context) ([]string, error)
	// Records lists records of one zone (bounded by limit).
	Records(ctx context.Context, zone string, limit int) ([]Record, error)
	// ApplyChange applies ONE typed change (create/update/delete) and
	// returns the resulting record (delete → zero Record).
	ApplyChange(ctx context.Context, zone string, ch Change) (Record, error)
}

// Stable errors (mapped to task error categories by executors).
var (
	ErrNotFound      = errors.New("dns: kayıt bulunamadı")
	ErrConflict      = errors.New("dns: kayıt çakışması")
	ErrTTLBounds     = errors.New("dns: TTL sağlayıcı sınırları dışında")
	ErrInvalidRecord = errors.New("dns: kayıt geçersiz")
	ErrInvalidZone   = errors.New("dns: bölge adı geçersiz")
	ErrTypeUnsupport = errors.New("dns: kayıt türü desteklenmiyor")
	ErrLimit         = errors.New("dns: sınır aşıldı")
	ErrUnknownKind   = errors.New("dns: bilinmeyen sağlayıcı türü")
	ErrAPI           = errors.New("dns: sağlayıcı API hatası")
)

// Limits (mirrored in tasks/ops_faz09.go).
const (
	MaxZones     = 500
	MaxRecords   = 500
	MaxNameParts = 10
	MaxLabelLen  = 63
	MaxNameLen   = 253
	MaxValueLen  = 2048
	MaxProviders = 50
)

// labelRE is one DNS label (per-label allowlist, mirrors web/ssl naming).
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidZoneName enforces lowercase FQDN-ish zone names without trailing dot.
func ValidZoneName(zone string) bool {
	if zone == "" || len(zone) > MaxNameLen || strings.Contains(zone, "..") {
		return false
	}
	for _, label := range strings.Split(zone, ".") {
		if !labelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidRecordName validates a record name relative to the zone:
// "@" (apex), "*", "<label-chain>" (no dots beyond zone-relative chains),
// "*.sub" wildcard when the provider declares wildcard support.
func ValidRecordName(name string, wildcard bool) bool {
	if name == "@" {
		return true
	}
	if name == "" || len(name) > MaxNameLen || strings.Contains(name, "..") {
		return false
	}
	rest := name
	if strings.HasPrefix(name, "*.") {
		if !wildcard {
			return false
		}
		rest = name[2:]
	}
	if rest == "" || strings.HasSuffix(rest, ".") {
		return false
	}
	for _, label := range strings.Split(rest, ".") {
		if !labelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidateTTL enforces the provider-declared TTL bounds.
func ValidateTTL(caps Capabilities, ttl int) error {
	if ttl < caps.TTLMin || ttl > caps.TTLMax {
		return fmt.Errorf("%w: %d (sağlayıcı sınırı %d..%d)", ErrTTLBounds, ttl, caps.TTLMin, caps.TTLMax)
	}
	return nil
}

// ValidateValue enforces per-type value shapes (closed set).
func ValidateValue(typ, value string) error {
	if value == "" || len(value) > MaxValueLen {
		return fmt.Errorf("%w: değer boş veya %d karakterden uzun", ErrInvalidRecord, MaxValueLen)
	}
	switch typ {
	case "A":
		return validIPv4(value)
	case "AAAA":
		return validIPv6(value)
	case "CNAME":
		if !validHostname(value) {
			return fmt.Errorf("%w: CNAME hedefi geçersiz: %q", ErrInvalidRecord, value)
		}
	case "TXT":
		// Free text, but reject control characters.
		for _, r := range value {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("%w: TXT kontrol karakteri içeriyor", ErrInvalidRecord)
			}
		}
	case "MX":
		parts := strings.Fields(value)
		if len(parts) != 2 {
			return fmt.Errorf("%w: MX 'öncelik hedef' biçiminde olmalı", ErrInvalidRecord)
		}
		prio, err := strconv.Atoi(parts[0])
		if err != nil || prio < 0 || prio > 65535 {
			return fmt.Errorf("%w: MX önceliği 0..65535 olmalı", ErrInvalidRecord)
		}
		if !validHostname(parts[1]) {
			return fmt.Errorf("%w: MX hedefi geçersiz: %q", ErrInvalidRecord, parts[1])
		}
	default:
		return ErrTypeUnsupport
	}
	return nil
}

var (
	ipv4RE = regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$`)
)

func validIPv4(v string) error {
	m := ipv4RE.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("%w: A kaydı IPv4 olmalı: %q", ErrInvalidRecord, v)
	}
	for i := 1; i <= 4; i++ {
		n, err := strconv.Atoi(m[i])
		if err != nil || n < 0 || n > 255 {
			return fmt.Errorf("%w: A sekizlisi 0..255 olmalı: %q", ErrInvalidRecord, v)
		}
	}
	return nil
}

func validIPv6(v string) error {
	// Standart kütüphane ayrıştırıcı — ayrışmıyorsa geçersiz.
	if net.ParseIP(v) == nil || !strings.Contains(v, ":") {
		return fmt.Errorf("%w: AAAA kaydı IPv6 olmalı: %q", ErrInvalidRecord, v)
	}
	return nil
}

func validHostname(h string) bool {
	if h == "" || len(h) > MaxNameLen || strings.Contains(h, "..") || strings.HasSuffix(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if !labelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidateRecord enforces name/type/value/TTL against provider capabilities.
func ValidateRecord(caps Capabilities, r Record) error {
	if !ValidRecordName(r.Name, caps.Wildcard) {
		return fmt.Errorf("%w: kayıt adı geçersiz: %q", ErrInvalidRecord, r.Name)
	}
	okType := false
	for _, t := range caps.RecordTypes {
		if r.Type == t {
			okType = true
			break
		}
	}
	if !okType {
		return fmt.Errorf("%w: %q", ErrTypeUnsupport, r.Type)
	}
	if err := ValidateValue(r.Type, r.Value); err != nil {
		return err
	}
	return ValidateTTL(caps, r.TTL)
}

// Diff computes the change a proposed record implies against current state.
// Mock/in-memory generic implementation used by the diff preview executor:
//   - same name+type exists → "update" (Before = existing)
//   - not present           → "create"
//
// A delete is requested explicitly (action=delete) and validated here.
func Diff(existing []Record, proposed Record) (Change, error) {
	for i := range existing {
		e := existing[i]
		if e.Name == proposed.Name && e.Type == proposed.Type {
			if e.Value == proposed.Value && e.TTL == proposed.TTL {
				return Change{Action: "update", Before: &e, After: &proposed}, nil // no-op still an update preview
			}
			return Change{Action: "update", Before: &e, After: &proposed}, nil
		}
	}
	return Change{Action: "create", Before: nil, After: &proposed}, nil
}
