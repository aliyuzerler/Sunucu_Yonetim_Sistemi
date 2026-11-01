package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 06: Linux dağıtım adaptörleri (Rocky/Alma) ----
//
// Şemalar agent/internal/pkgops ve agent/internal/fwops ile AYNA şeklinde
// tutulur (payload'lar iki tarafta da doğrulanır — proto sözleşmesi).
// Güvenlik: dört işlem de ClassReadOnly'dir; firewall kural değişikliği
// yüzeyi bu fazda YOKTUR — linux.firewall.rule.preview yalnız komut
// ÖNİZLEMESİ döndürür, hiçbir şey çalıştırmaz (ADR-0010).

type pkgListPayload struct {
	Limit int `json:"limit,omitempty"`
}

type fwPreviewPayload struct {
	Zone     string `json:"zone,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Action   string `json:"action"`
}

// fwZoneRE mirrors agent/internal/fwops: firewalld zone names are the only
// user-controlled string in the preview payload (strict allowlist).
var fwZoneRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,32}$`)

func fwZoneShape(z string) bool { return fwZoneRE.MatchString(z) }

// Closed enums mirror agent/internal/fwops (Action/Protocol).
var fwActions = map[string]bool{"allow": true, "revoke": true}
var fwProtocols = map[string]bool{"tcp": true, "udp": true}

// ValidatePkgList enforces {} or {"limit": 0..500}.
func ValidatePkgList(payload json.RawMessage) error {
	var p pkgListPayload
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > 500 {
		return errors.New("limit 0..500 olmalı")
	}
	return nil
}

// ValidateFirewallStatus enforces {} (or absent payload).
func ValidateFirewallStatus(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var p struct{}
	if err := decodeStrict(payload, &p); err != nil {
		return errors.New("status şeması uyumsuz (boş obje beklenir)")
	}
	return nil
}

// ValidateFirewallPreview enforces the closed enums + bounded ints; zone
// allowlist mirrors fwops.ValidZone.
func ValidateFirewallPreview(payload json.RawMessage) error {
	var p fwPreviewPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !fwZoneShape(p.Zone) {
		return errors.New("zone biçimi [A-Za-z0-9_.-]{0,32} olmalı")
	}
	if p.Port < 1 || p.Port > 65535 {
		return errors.New("port 1..65535 olmalı")
	}
	if !fwProtocols[p.Protocol] {
		return errors.New("protocol tcp|udp olmalı")
	}
	if !fwActions[p.Action] {
		return errors.New("action allow|revoke olmalı")
	}
	return nil
}

// buildFaz06Registry returns the Faz 06 operation specs.
func buildFaz06Registry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name: "linux.pkg.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidatePkgList,
			RequiredPerm: rbac.PermPkgRead,
		},
		{
			Name: "linux.firewall.status", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 64,
			Validate:     ValidateFirewallStatus,
			RequiredPerm: rbac.PermFirewallRead,
		},
		{
			Name: "linux.firewall.rules.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidatePkgList, // shared {limit} schema
			RequiredPerm: rbac.PermFirewallRead,
		},
		{
			Name: "linux.firewall.rule.preview", Version: "1.0.0",
			// Preview-only: renders commands as data, executes NOTHING
			// (ADR-0010). RetrySafe=true is honest — the operation has no
			// side effects to repeat.
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateFirewallPreview,
			RequiredPerm: rbac.PermFirewallRead,
		},
	}
}
