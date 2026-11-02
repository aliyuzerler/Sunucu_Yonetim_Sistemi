package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 13: Log arama (logs.search) ----
//
// Şema agent/internal/logops ile AYNA şeklinde tutulur (payload iki tarafta
// da doğrulanır). Güvenlik: salt-okuma; journald sorgusu SABİT argv
// şablonuyla kurulur (agent tarafı), birim adı allowlist'li, zamanlar
// RFC3339 doğrulamalı, metin filtresi uzunluk sınırlı — kullanıcı girdisi
// asla shell/komut metnine dönmez.

type logSearchPayload struct {
	Unit     string `json:"unit,omitempty"`     // systemd unit (allowlist)
	Priority int    `json:"priority,omitempty"` // 0..7 journald önceliği (0 = filtre yok)
	Since    string `json:"since,omitempty"`    // RFC3339
	Until    string `json:"until,omitempty"`    // RFC3339
	Contains string `json:"contains,omitempty"` // serbest metin filtresi (redaction SONRASI eşleşir)
	Limit    int    `json:"limit,omitempty"`    // 0..500 (0 = 200)
}

// unitNameRE mirrors the systemd unit allowlist (ops.go ValidateServiceUnit
// uses the same class; duplicated here to keep faz13 self-contained).
var logUnitRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`)

// rfc3339RE is a cheap shape check; full parse happens below.
var logTimeShapeRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)

// maxContains bounds the free-text filter (not a label value, not a shell
// fragment — purely a substring filter applied after redaction).
const maxContains = 256

// ValidateLogSearch mirrors agent/internal/logops validation.
func ValidateLogSearch(payload json.RawMessage) error {
	var p logSearchPayload
	if len(payload) == 0 {
		return nil
	}
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Unit != "" && !logUnitRE.MatchString(p.Unit) {
		return errors.New("unit geçersiz systemd birim adı olmalı (örn. nginx.service)")
	}
	if p.Priority < 0 || p.Priority > 7 {
		return errors.New("priority 0..7 olmalı")
	}
	for name, ts := range map[string]string{"since": p.Since, "until": p.Until} {
		if ts == "" {
			continue
		}
		if !logTimeShapeRE.MatchString(ts) {
			return fmt.Errorf("%s RFC3339 biçiminde olmalı", name)
		}
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			return fmt.Errorf("%s geçersiz zaman: %v", name, err)
		}
	}
	if len(p.Contains) > maxContains {
		return fmt.Errorf("contains en fazla %d karakter olmalı", maxContains)
	}
	if p.Contains != "" {
		for _, r := range p.Contains {
			if r < 0x20 && r != '\t' {
				return errors.New("contains kontrol karakteri içeremez")
			}
		}
	}
	if p.Limit < 0 || p.Limit > 500 {
		return errors.New("limit 0..500 olmalı")
	}
	if p.Since != "" && p.Until != "" {
		s, _ := time.Parse(time.RFC3339, p.Since)
		u, _ := time.Parse(time.RFC3339, p.Until)
		if u.Before(s) {
			return errors.New("until, since'den önce olamaz")
		}
	}
	return nil
}

// buildFaz13Registry returns the Faz 13 operation specs.
func buildFaz13Registry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name:         "logs.search",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateLogSearch,
			RequiredPerm: rbac.PermLogsRead,
		},
	}
}
