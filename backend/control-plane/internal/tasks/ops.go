package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// NoopExecutor echoes the payload back; it proves the engine end-to-end
// without touching any host.
type NoopExecutor struct{}

// Execute returns the payload verbatim.
func (NoopExecutor) Execute(_ context.Context, t *Task) (map[string]any, string, error) {
	var p map[string]any
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
	}
	return map[string]any{"echo": p, "operation": t.Operation}, "noop tamamlandı", nil
}

// ValidateNoopPayload accepts any JSON object up to the spec limit.
func ValidateNoopPayload(payload json.RawMessage) error {
	var v any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("geçerli JSON bekleniyor: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return errors.New("JSON nesnesi bekleniyor")
	}
	return nil
}

// TagSetter is the narrow inventory surface the tag executor needs.
type TagSetter interface {
	SetTags(ctx context.Context, actor, correlationID, nodeID string, tags []string) (*inventory.Node, error)
}

// SetTagsExecutor applies idempotent desired-state tags to a node. It is the
// first typed operation backed by real domain logic; agent-dispatched
// operations follow the same executor contract in Faz 03+.
type SetTagsExecutor struct {
	Setter TagSetter
}

// tagPayload is the typed request schema.
type tagPayload struct {
	NodeID string   `json:"node_id"`
	Tags   []string `json:"tags"`
}

// Execute applies tags via the inventory service (which audits node.tag).
func (e SetTagsExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	var p tagPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, "", fmt.Errorf("payload çözülemedi: %w", err)
	}
	nodeID := p.NodeID
	if t.NodeID != nil && p.NodeID == "" {
		nodeID = *t.NodeID
	}
	n, err := e.Setter.SetTags(ctx, t.Actor, t.CorrelationID, nodeID, p.Tags)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{
		"node_id": n.ID,
		"tags":    n.Tags,
	}, "etiketler uygulandı", nil
}

// ValidateSetTagsPayload enforces the typed schema {node_id, tags[]}.
func ValidateSetTagsPayload(payload json.RawMessage) error {
	var p tagPayload
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.NodeID == "" {
		return errors.New("node_id zorunlu")
	}
	if len(p.Tags) == 0 || len(p.Tags) > 32 {
		return errors.New("tags 1..32 öğe olmalı")
	}
	return nil
}

// ---- Faz 04: linux systemd servis operasyonları (node hedefli) ----

// unitNameRE mirrors agent/internal/svcops: first char alphanumeric (no flag
// injection), template units allowed, no path separators or shell
// metacharacters, mandatory .service suffix, UNIT_NAME_MAX=256 bounded.
var unitNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$`)

// ValidateServiceUnit enforces the server-side unit allowlist (payloads are
// validated on BOTH sides — proto contract).
func ValidateServiceUnit(payload json.RawMessage) error {
	var p serviceUnitPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !unitNameRE.MatchString(p.Unit) {
		return errors.New("unit adı allowlist dışı: [A-Za-z0-9] ile başlamalı, .service ile bitmeli, boşluk/yol/shell karakteri içeremez")
	}
	return nil
}

// ValidateServiceListPayload enforces {} or {"limit": 1..500}.
func ValidateServiceListPayload(payload json.RawMessage) error {
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

type serviceUnitPayload struct {
	Unit string `json:"unit"`
}

type serviceListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// decodeStrict is the shared strict JSON decoder for payload validators.
func decodeStrict(payload json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("sonrası veri")
	}
	return nil
}

// BuildDefaultRegistry returns the Faz 02 operation registry (Faz 04-09
// ile genişletildi). faz09 nil geçilebilir: DNS/cert executor'ları o zaman
// dürüst yapılandırma hatası döndürür.
func BuildDefaultRegistry(tagSetter TagSetter, faz09 Faz09Services) (*Registry, error) {
	specs := []*OperationSpec{
		&OperationSpec{
			Name:       "noop.echo",
			Version:    "1.0.0",
			Class:      ClassReadOnly,
			Target:     TargetControlPlane,
			RetrySafe:  true,
			Timeout:    30 * time.Second,
			MaxPayload: 4 << 10,
			Validate:   ValidateNoopPayload,
			Exec:       NoopExecutor{},
		},
		&OperationSpec{
			Name:       "inventory.node.tag.set",
			Version:    "1.0.0",
			Class:      ClassIdempotent,
			Target:     TargetControlPlane,
			RetrySafe:  true,
			Timeout:    30 * time.Second,
			MaxPayload: 8 << 10,
			Validate:   ValidateSetTagsPayload,
			Exec:       SetTagsExecutor{Setter: tagSetter},
		},
		&OperationSpec{
			Name:       "agent.echo",
			Version:    "1.0.0",
			Class:      ClassReadOnly,
			Target:     TargetNode,
			RetrySafe:  true,
			Timeout:    30 * time.Second,
			MaxPayload: 1 << 10,
			Validate:   ValidateAgentEchoPayload,
		},

		// Faz 04: servis işlemleri. Broker + agent tarafında allowlist,
		// control-plane'de RBAC katmanı (service:read / service:control).
		&OperationSpec{
			Name:         "linux.systemd.service.list",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateServiceListPayload,
			RequiredPerm: rbac.PermServiceRead,
		},
		&OperationSpec{
			Name:         "linux.systemd.service.status",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateServiceUnit,
			RequiredPerm: rbac.PermServiceRead,
		},
		&OperationSpec{
			Name:         "linux.systemd.service.start",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateServiceUnit,
			RequiredPerm: rbac.PermServiceControl,
		},
		&OperationSpec{
			Name:         "linux.systemd.service.stop",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateServiceUnit,
			RequiredPerm: rbac.PermServiceControl,
		},
		&OperationSpec{
			Name:         "linux.systemd.service.restart",
			Version:      "1.0.0",
			Class:        ClassNonIdem,
			Target:       TargetNode,
			RetrySafe:    false,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateServiceUnit,
			RequiredPerm: rbac.PermServiceControl,
		},

		// Faz 05: Windows SCM servis işlemleri + Event Log okuma. Ayrı
		// aile; şemalar agent/internal/winops ile aynadır (iki taraf da
		// doğrular). PowerShell yürütme yüzeyi YOKTUR (ADR-0009).
		&OperationSpec{
			Name:         "windows.scm.service.list",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateWinServiceList,
			RequiredPerm: rbac.PermServiceRead,
		},
		&OperationSpec{
			Name:         "windows.scm.service.status",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceRead,
		},
		&OperationSpec{
			Name:         "windows.scm.service.start",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		&OperationSpec{
			Name:         "windows.scm.service.stop",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		&OperationSpec{
			Name:         "windows.scm.service.restart",
			Version:      "1.0.0",
			Class:        ClassNonIdem,
			Target:       TargetNode,
			RetrySafe:    false,
			Timeout:      3 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateWinServiceName,
			RequiredPerm: rbac.PermServiceControl,
		},
		&OperationSpec{
			Name:         "windows.eventlog.read",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateEventlogRead,
			RequiredPerm: rbac.PermEventlogRead,
		},
	}
	// Faz 06: Linux dağıtım adaptörleri (paket envanteri + firewall
	// sörvey/önizleme) ayrı dosyada: ops_faz06.go.
	specs = append(specs, buildFaz06Registry()...)
	// Faz 07: Docker Engine + Compose yönetimi ayrı dosyada: ops_faz07.go
	// (remove ailesi preview-only — ADR-0011).
	specs = append(specs, buildFaz07Registry()...)
	// Faz 08: Web hosting (nginx + PHP-FPM site yaşam döngüsü) ayrı dosyada:
	// ops_faz08.go (delete dosya-koruyucu — ADR-0012).
	specs = append(specs, buildFaz08Registry()...)
	// Faz 09: DNS sağlayıcı adaptörü + ACME sertifika yaşam döngüsü ayrı
	// dosyada: ops_faz09.go (anahtar-node'da, kimlik-şifreli — ADR-0013).
	specs = append(specs, buildFaz09Registry(faz09)...)
	return NewRegistry(specs...)
}

// agentEchoPayload is the typed schema for the node-side echo operation; it
// proves the mTLS dispatch loop without touching the host (Faz 03).
type agentEchoPayload struct {
	Message string `json:"message"`
}

// ValidateAgentEchoPayload enforces {message: string(<=256)}.
func ValidateAgentEchoPayload(payload json.RawMessage) error {
	var p agentEchoPayload
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if len(p.Message) > 256 {
		return errors.New("message en fazla 256 karakter")
	}
	return nil
}
