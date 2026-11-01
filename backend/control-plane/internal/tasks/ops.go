package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
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

// BuildDefaultRegistry returns the Faz 02 operation registry.
func BuildDefaultRegistry(tagSetter TagSetter) (*Registry, error) {
	return NewRegistry(
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
	)
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
