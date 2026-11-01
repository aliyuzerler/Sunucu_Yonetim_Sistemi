package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 07: Docker Engine ve Compose yönetimi ----
//
// Şemalar agent/internal/dockerx ve agent/internal/composeops ile AYNA
// şeklinde tutulur (payload'lar iki tarafta da doğrulanır — proto sözleşmesi).
//
// Güvenlik modeli (ADR-0011):
//   - Docker daemon'a erişim YALNIZ agent'ın privileged broker'ındadır
//     (unix socket; TCP yok).
//   - container/image/volume/network remove ile compose.deploy PREVIEW-ONLY
//     operasyonlardır: yürütme yüzeyleri yoktur, Executed=false değişmezi;
//     Class=ClassReadOnly + RetrySafe=true dürüsttür (yan etkisiz).
//   - container start/stop idempotent_desired_state; restart non_idempotent
//     + retry-unsafe (systemd/windows SCM kalıbının aynısı).

// refRE mirrors agent/internal/dockerx refRE (container/network/volume).
var dockerRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// dockerImageRefRE mirrors dockerx imageRefRE (tags/digests/registry paths).
var dockerImageRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,199}$`)

func dockerValidRef(ref string) bool {
	if !dockerRefRE.MatchString(ref) {
		return false
	}
	for i := 0; i+1 < len(ref); i++ {
		if ref[i] == '.' && ref[i+1] == '.' {
			return false
		}
	}
	return true
}

func dockerValidImageRef(ref string) bool {
	if !dockerImageRefRE.MatchString(ref) {
		return false
	}
	for i := 0; i+1 < len(ref); i++ {
		if ref[i] == '.' && ref[i+1] == '.' {
			return false
		}
	}
	return true
}

// dockerRefPayload is the {ref} schema (inspect/start).
type dockerRefPayload struct {
	Ref string `json:"ref"`
}

// dockerLogsPayload is the {ref, tail} schema.
type dockerLogsPayload struct {
	Ref  string `json:"ref"`
	Tail int    `json:"tail,omitempty"`
}

// dockerActionPayload is the {ref, timeout} schema (stop/restart).
type dockerActionPayload struct {
	Ref     string `json:"ref"`
	Timeout int    `json:"timeout,omitempty"`
}

// dockerRemovePayload mirrors dockerx.RemoveParams (strict decode).
type dockerRemovePayload struct {
	Ref   string `json:"ref"`
	Force bool   `json:"force,omitempty"`
	Link  bool   `json:"link,omitempty"`
	V     bool   `json:"v,omitempty"`
	Prune bool   `json:"prune,omitempty"`
}

// dockerComposePayload is the {compose} envelope; deep analysis is agent-side.
type dockerComposePayload struct {
	Compose string `json:"compose"`
}

// MaxDockerComposeBytes mirrors composeops.MaxComposeBytes.
const MaxDockerComposeBytes = 64 << 10

// ValidateDockerList enforces {} or {"limit": 0..500} (shared list schema).
func ValidateDockerList(payload json.RawMessage) error {
	var p struct {
		Limit int `json:"limit,omitempty"`
	}
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

// ValidateDockerRef enforces the container/network/volume ref shape.
func ValidateDockerRef(payload json.RawMessage) error {
	var p dockerRefPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dockerValidRef(p.Ref) {
		return errors.New("ref biçimi [A-Za-z0-9][A-Za-z0-9_.-]{0,127} olmalı")
	}
	return nil
}

// ValidateDockerLogs enforces {ref, tail 1..500}.
func ValidateDockerLogs(payload json.RawMessage) error {
	var p dockerLogsPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dockerValidRef(p.Ref) {
		return errors.New("ref biçimi hatalı")
	}
	if p.Tail == 0 {
		p.Tail = 100
	}
	if p.Tail < 1 || p.Tail > 500 {
		return errors.New("tail 1..500 olmalı")
	}
	return nil
}

// ValidateDockerAction enforces {ref, timeout 1..120}.
func ValidateDockerAction(payload json.RawMessage) error {
	var p dockerActionPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !dockerValidRef(p.Ref) {
		return errors.New("ref biçimi hatalı")
	}
	if p.Timeout == 0 {
		p.Timeout = 10
	}
	if p.Timeout < 1 || p.Timeout > 120 {
		return errors.New("timeout 1..120 olmalı")
	}
	return nil
}

// ValidateDockerRemoveByKind enforces the kind-specific ref shape.
func ValidateDockerRemoveByKind(kind string) func(json.RawMessage) error {
	return func(payload json.RawMessage) error {
		var p dockerRemovePayload
		if err := decodeStrict(payload, &p); err != nil {
			return fmt.Errorf("şema uyumsuz: %w", err)
		}
		valid := dockerValidRef(p.Ref)
		if kind == "image" {
			valid = dockerValidImageRef(p.Ref)
		}
		if !valid {
			return errors.New("ref biçimi hatalı")
		}
		return nil
	}
}

// ValidateDockerCompose enforces the {compose} envelope bounds (deep policy
// analysis runs agent-side in composeops).
func ValidateDockerCompose(payload json.RawMessage) error {
	var p dockerComposePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if len(p.Compose) == 0 {
		return errors.New("compose boş")
	}
	if len(p.Compose) > MaxDockerComposeBytes {
		return fmt.Errorf("compose %d bayt > %d sınırı", len(p.Compose), MaxDockerComposeBytes)
	}
	return nil
}

// buildFaz07Registry returns the Faz 07 operation specs.
func buildFaz07Registry() []*OperationSpec {
	return []*OperationSpec{
		{
			Name: "docker.container.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerList,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.container.inspect", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRef,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.container.logs", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 90 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerLogs,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.container.start", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRef,
			RequiredPerm: rbac.PermDockerControl,
		},
		{
			Name: "docker.container.stop", Version: "1.0.0",
			Class: ClassIdempotent, Target: TargetNode, RetrySafe: true,
			Timeout: 2 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateDockerAction,
			RequiredPerm: rbac.PermDockerControl,
		},
		{
			Name: "docker.container.restart", Version: "1.0.0",
			// non_idempotent + retry-unsafe: systemd/SCM restart kalıbı.
			Class: ClassNonIdem, Target: TargetNode, RetrySafe: false,
			Timeout: 3 * time.Minute, MaxPayload: 1 << 10,
			Validate:     ValidateDockerAction,
			RequiredPerm: rbac.PermDockerControl,
		},
		{
			Name: "docker.container.remove", Version: "1.0.0",
			// PREVIEW-ONLY (ADR-0011): Engine DELETE çağrısı DATA olarak
			// döner; yürütme yüzeyi yoktur (agent tipinde remove metodu bile
			// yoktur). Yan etkisiz → ClassReadOnly + RetrySafe dürüsttür.
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRemoveByKind("container"),
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.image.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerList,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.image.remove", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true, // preview-only
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRemoveByKind("image"),
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.network.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerList,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.network.remove", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true, // preview-only
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRemoveByKind("network"),
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.volume.list", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerList,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.volume.remove", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true, // preview-only
			Timeout: 60 * time.Second, MaxPayload: 1 << 10,
			Validate:     ValidateDockerRemoveByKind("volume"),
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.compose.validate", Version: "1.0.0",
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: MaxDockerComposeBytes,
			Validate:     ValidateDockerCompose,
			RequiredPerm: rbac.PermDockerRead,
		},
		{
			Name: "docker.compose.deploy", Version: "1.0.0",
			// PREVIEW/PLAN-ONLY (ADR-0011): şema/policy raporu + aksiyon
			// planı DATA olarak döner; deploy YÜRÜTMEZ (Executed=false).
			Class: ClassReadOnly, Target: TargetNode, RetrySafe: true,
			Timeout: 60 * time.Second, MaxPayload: MaxDockerComposeBytes,
			Validate:     ValidateDockerCompose,
			RequiredPerm: rbac.PermDockerRead,
		},
	}
}
