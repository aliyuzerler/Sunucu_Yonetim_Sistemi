package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 05: Windows SCM servis + Event Log operasyonları (node hedefli) ----
//
// Şemalar agent/internal/winops ile AYNA şeklinde tutulur (payload'lar iki
// tarafta da doğrulanır — proto sözleşmesi). Güvenlik: servis adı allowlist
// regex; kanal kapalı enum; seviye/sayı sınırlı int. PowerShell yürütme
// yüzeyi YOKTUR (ADR-0009).

// scmNameRE mirrors agent/internal/winops: SCM service KEY names. First char
// alphanumeric (no argument injection), no path separators/shell
// metacharacters/quotes, 80 chars bound, no trailing dot/space.
var scmNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$`)

// validServiceName applies the regex plus the trailing dot/space rule.
func validServiceName(name string) bool {
	if !scmNameRE.MatchString(name) {
		return false
	}
	last := name[len(name)-1]
	return last != '.' && last != ' '
}

// eventChannels is the closed channel enum; Security is EXCLUDED by design
// (requires admin; the agent runs unprivileged — ADR-0009).
var eventChannels = map[string]bool{"Application": true, "System": true, "Setup": true}

type serviceWinPayload struct {
	Service string `json:"service"`
}

type eventlogPayload struct {
	Channel string `json:"channel"`
	Level   int    `json:"level,omitempty"`
	Count   int    `json:"count,omitempty"`
}

// ValidateWinServiceList enforces {} or {"limit": 0..500}.
func ValidateWinServiceList(payload json.RawMessage) error {
	var p serviceListPayload
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

// ValidateWinServiceName enforces {"service": <allowlist>}.
func ValidateWinServiceName(payload json.RawMessage) error {
	var p serviceWinPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !validServiceName(p.Service) {
		return errors.New("servis adı allowlist dışı: [A-Za-z0-9] ile başlamalı, boşluk/tire/nokta/alt çizgi dışında özel karakter içeremez, sondaki nokta/boşluk yasak")
	}
	return nil
}

// ValidateEventlogRead enforces the closed channel enum + bounded ints.
func ValidateEventlogRead(payload json.RawMessage) error {
	var p eventlogPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !eventChannels[p.Channel] {
		return errors.New("channel Application|System|Setup olmalı")
	}
	if p.Level < 0 || p.Level > 5 {
		return errors.New("level 0..5 olmalı")
	}
	if p.Count < 0 || p.Count > 200 {
		return errors.New("count 0..200 olmalı")
	}
	return nil
}

// buildWindowsRegistry returns the Faz 05 operation specs.
func buildWindowsRegistry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name: "windows.scm.service.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWinServiceList,
			RequiredPerm: rbac.PermServiceRead,
		},
		{
			Name: "windows.scm.service.status", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceRead,
		},
		{
			Name: "windows.scm.service.start", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		{
			Name: "windows.scm.service.stop", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		{
			Name: "windows.scm.service.restart", Version: "1.0.0",
			Class: ClassNonIdem, Target: TargetNode, RetrySafe: false,
			Timeout: 3 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		{
			Name: "windows.eventlog.read", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateEventlogRead,
			RequiredPerm: rbac.PermEventlogRead,
		},
	}
}
