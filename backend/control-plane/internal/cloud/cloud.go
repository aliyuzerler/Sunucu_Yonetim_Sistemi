// Package cloud implements Faz 14: the provider adapter interface and the
// first provider connector (S3-compatible object storage).
//
// Kriter 5 discipline:
//   - Every provider kind carries its OWN scope/credential/permission matrix
//     (PermissionMatrix) — nothing is shared implicitly between providers.
//   - Credential material is sealed (AES-256-GCM, internal/secrets) and never
//     returned by any read path.
//   - Resource deletion is CONFIRMATION-GATED at the service boundary
//     (ErrConfirmationRequired without confirm=true) and audited.
//   - Deletion candidates are validated against the provider (a non-empty
//     bucket is refused by S3; mapped to ErrResourceNotEmpty — the control
//     plane never force-deletes provider data).
package cloud

import (
	"context"
	"errors"
	"time"
)

// Kind is the closed enum of supported provider adapters. Each adapter is
// separately tested (feature-matrix: "Each provider separately tested").
type Kind string

// Supported provider kinds.
const (
	KindS3 Kind = "s3" // S3-compatible object storage (AWS S3, Hetzner, MinIO, ...)
)

// Action is a provider operation class used in the permission matrix.
type Action string

// Actions per provider matrix.
const (
	ActionList   Action = "list"
	ActionRead   Action = "read"
	ActionDelete Action = "delete"
)

// Risk classes for matrix entries.
const (
	RiskRead        = "read"
	RiskDestructive = "destructive"
)

// MatrixEntry documents one provider-side permission requirement.
type MatrixEntry struct {
	Action             Action `json:"action"`
	ProviderPermission string `json:"provider_permission"`
	Risk               string `json:"risk"`
	ConfirmRequired    bool   `json:"confirm_required"`
}

// PermissionMatrix is the per-provider scope/permission declaration. It ships
// with the adapter and is surfaced verbatim to the UI (honest capability
// documentation — scope.md: never claim coverage without checking the
// provider's permission model).
type PermissionMatrix struct {
	Provider Kind          `json:"provider"`
	Entries  []MatrixEntry `json:"entries"`
}

// ProbeResult reports one credential probe outcome (no secrets, short reason).
type ProbeResult struct {
	Action Action `json:"action"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// VerifyReport is the outcome of a credential verification run.
type VerifyReport struct {
	Provider  Kind             `json:"provider"`
	Endpoint  string           `json:"endpoint"`
	Region    string           `json:"region"`
	Matrix    PermissionMatrix `json:"matrix"`
	Probes    []ProbeResult    `json:"probes"`
	CheckedAt string           `json:"checked_at"`
}

// Resource is one provider-side resource visible to the credential.
type Resource struct {
	Name  string            `json:"name"`
	Kind  string            `json:"kind"`
	Extra map[string]string `json:"extra,omitempty"`
}

// Credentials is the decrypted credential material handed to adapters.
type Credentials struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// Provider is the adapter contract. Implementations MUST be side-effect free
// except for the explicitly destructive DeleteResource, which the service
// layer calls only after the confirmation gate.
type Provider interface {
	Kind() Kind
	// PermissionMatrix declares the provider-side permission model.
	PermissionMatrix() PermissionMatrix
	// RequiredCredentialKeys lists the credential field names this provider
	// requires (validated at registration).
	RequiredCredentialKeys() []string
	// VerifyCredentials probes the credential with read-only calls only.
	VerifyCredentials(ctx context.Context, c Credentials) (VerifyReport, error)
	// ListResources returns visible resources (bounded by limit).
	ListResources(ctx context.Context, c Credentials, limit int) ([]Resource, error)
	// DeleteResource destroys one resource. Callers must enforce the
	// confirmation gate BEFORE calling this.
	DeleteResource(ctx context.Context, c Credentials, name string) error
}

// Errors mapped to stable API responses.
var (
	ErrProviderNotFound     = errors.New("cloud: sağlayıcı bulunamadı")
	ErrProviderLimit        = errors.New("cloud: sağlayıcı sınırına ulaşıldı (en fazla 20)")
	ErrUnknownKind          = errors.New("cloud: bilinmeyen sağlayıcı türü")
	ErrInvalidName          = errors.New("cloud: sağlayıcı adı 3..64 karakter olmalı")
	ErrInvalidEndpoint      = errors.New("cloud: uç nokta geçersiz (https zorunlu; yalnız döngü adresi http olabilir)")
	ErrCredIncomplete       = errors.New("cloud: kimlik bilgisi eksik")
	ErrKeyNotConfigured     = errors.New("cloud: şifreleme anahtarı yapılandırılmadı (CP_SECRETS_KEY)")
	ErrConfirmationRequired = errors.New("cloud: silme onayı zorunlu (confirm=true)")
	ErrResourceNotFound     = errors.New("cloud: kaynak sağlayıcıda bulunamadı")
	ErrResourceNotEmpty     = errors.New("cloud: kaynak boş değil — panel zorla silmez")
	ErrPermission           = errors.New("cloud: kimlik bilgisi bu işlem için yetkili değil")
	ErrProviderUnreachable  = errors.New("cloud: sağlayıcıya ulaşılamadı")
	ErrResourceNameInvalid  = errors.New("cloud: kaynak adı geçersiz")
)

// NowFunc is overridable for tests.
type NowFunc func() time.Time
