package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
)

// ---- Faz 15: imzalı güncelleme (ADR-0020) ----
//
// update.* ailesi node hedeflidir ve broker ailesi --update-enable ile
// opt-in'dir (varsayılan KAPALI). Şemalar agent/internal/updateops ile
// AYNA şeklinde tutulur (iki taraf da doğrular). Control Plane yalnız
// üst veri kaydeder; artifact yerelde doğrulanır (sha256 + ed25519 imza),
// staging + health-check + rollback node'da yapılır. update.stage
// oluşturulurken artifact kaydı updates kayıt defterinde VAR ve PUBLISHED
// olmak ZORUNDADIR (validator kapanışı — iptal edilmiş artifact için görev
// oluşturulamaz; kriter 2).

// Faz15Updates is the Control Plane update registry contract the validator
// closure needs (implemented by *updates.Service; faked in tests).
type Faz15Updates interface {
	Stageable(ctx context.Context, id, version, sha256hex, signature string) error
}

// Faz15Services bundles the optional Faz 15 dependencies. A nil Updates means
// validators check payload shape only (unit-test builds); production wiring
// always provides the service.
type Faz15Services struct {
	Updates Faz15Updates
}

var (
	uuidFaz15RE  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shaFaz15RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	verFaz15RE   = regexp.MustCompile(`^[0-9A-Za-z.+-]{5,64}$`)
	sigFaz15RE   = regexp.MustCompile(`^[A-Za-z0-9+/]{86,344}={0,2}$`)
	emptyPayload = regexp.MustCompile(`^\s*$`)
)

func decodeStrictFaz15(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("payload şeması uyumsuz: %w", err)
	}
	return nil
}

// updateStagePayload mirrors agent/internal/updateops.StagePayload.
type updateStagePayload struct {
	UpdateID  string `json:"update_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// ValidateUpdateStage enforces the strict schema and, when the updates
// registry is wired, the published-artifact gate (revoked/unknown → reject).
func ValidateUpdateStage(svc Faz15Updates) func(json.RawMessage) error {
	return func(payload json.RawMessage) error {
		var p updateStagePayload
		if err := decodeStrictFaz15(payload, &p); err != nil {
			return err
		}
		if !uuidFaz15RE.MatchString(p.UpdateID) {
			return errors.New("update_id geçerli bir UUID olmalı")
		}
		if !verFaz15RE.MatchString(p.Version) {
			return errors.New("version 5..64 karakter olmalı")
		}
		if !shaFaz15RE.MatchString(p.SHA256) {
			return errors.New("sha256 64 küçük-hex olmalı")
		}
		if !sigFaz15RE.MatchString(p.Signature) {
			return errors.New("signature base64 ed25519 olmalı")
		}
		if svc == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := svc.Stageable(ctx, p.UpdateID, p.Version, p.SHA256, p.Signature); err != nil {
			return fmt.Errorf("update artifact doğrulanamadı: %w", err)
		}
		return nil
	}
}

// ValidateUpdateEmpty enforces the strictly empty schema (status/rollback:
// the target path, version and binary names are node-managed, never
// payload-supplied).
func ValidateUpdateEmpty(payload json.RawMessage) error {
	if emptyPayload.MatchString(string(payload)) {
		return nil
	}
	var p map[string]json.RawMessage
	if err := decodeStrictFaz15(payload, &p); err != nil {
		return err
	}
	if len(p) != 0 {
		return errors.New("payload boş olmalı")
	}
	return nil
}

func buildFaz15Registry(faz15 Faz15Services) []*OperationSpec {
	return []*OperationSpec{
		{
			Name:         "update.stage",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   2048,
			Validate:     ValidateUpdateStage(faz15.Updates),
			RequiredPerm: rbac.PermUpdateCtrl,
		},
		{
			Name:         "update.status",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      30 * time.Second,
			MaxPayload:   512,
			Validate:     ValidateUpdateEmpty,
			RequiredPerm: rbac.PermUpdateRead,
		},
		{
			Name:         "update.rollback",
			Version:      "1.0.0",
			Class:        ClassDestructive,
			Target:       TargetNode,
			RetrySafe:    false,
			Timeout:      2 * time.Minute,
			MaxPayload:   512,
			Validate:     ValidateUpdateEmpty,
			RequiredPerm: rbac.PermUpdateCtrl,
		},
	}
}
