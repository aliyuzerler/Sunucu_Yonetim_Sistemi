package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

// Extension is the stored record: signed manifest + administrative state.
type Extension struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	DisplayName  string          `json:"display_name"`
	Vendor       string          `json:"vendor"`
	Version      string          `json:"version"`
	APIVersion   string          `json:"api_version"`
	MinCPVersion string          `json:"min_cp_version"`
	ArtifactSHA  string          `json:"artifact_sha256"`
	State        string          `json:"state"` // pending | approved | revoked
	Manifest     json.RawMessage `json:"manifest"`
	Requested    []PermRequest   `json:"requested_permissions"`
	Granted      []Grant         `json:"granted_permissions"`
	DelegatedOps []string        `json:"delegated_ops"`
	Capabilities []string        `json:"capabilities"`
	Compatible   bool            `json:"api_compatible"`
	RegisteredBy string          `json:"registered_by"`
	RegisteredAt time.Time       `json:"registered_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Grant is ONE explicitly approved permission (no defaults exist).
type Grant struct {
	Action    string     `json:"action"`
	Risk      string     `json:"risk"`
	GrantedBy string     `json:"granted_by"`
	GrantedAt time.Time  `json:"granted_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Store persists extensions and grants.
type Store interface {
	Upsert(ctx context.Context, e Extension, manifest json.RawMessage) (Extension, error)
	Get(ctx context.Context, id string) (Extension, json.RawMessage, error)
	GetByName(ctx context.Context, name string) (Extension, json.RawMessage, error)
	List(ctx context.Context) ([]Extension, error)
	UpsertGrant(ctx context.Context, extID string, g Grant) (Grant, error)
	RevokeGrant(ctx context.Context, extID, action string) error
	RevokeAllGrants(ctx context.Context, extID string) error
}

// Service validates and records extensions (ADR-0019). The service holds NO
// write authority beyond this contract: it never grants permissions implicitly.
type Service struct {
	store Store
	// trust holds the ed25519 public keys (raw 32 bytes) accepted for
	// manifest signatures. Empty → registration is permanently closed
	// (fail-closed, honest config error).
	trust [][]byte
	// apiVersion / cpVersion describe the running control plane for
	// compatibility checks.
	apiVersion string
	cpVersion  string
	// knownOps reports the currently registered typed task operations;
	// delegated_ops must be a subset (validated at registration AND approval).
	knownOps func() map[string]bool
	// catalog reports the valid permission actions an extension may request.
	catalog func() map[string]bool
}

// NewService builds the extension service. trustB64 entries are base64
// ed25519 public keys; invalid entries abort construction (config error).
func NewService(store Store, trustB64 []string, apiVersion, cpVersion string, knownOps func() map[string]bool, catalog func() map[string]bool) (*Service, error) {
	s := &Service{store: store, apiVersion: apiVersion, cpVersion: cpVersion, knownOps: knownOps, catalog: catalog}
	for _, b := range trustB64 {
		if strings.TrimSpace(b) == "" {
			continue
		}
		key, err := signpkg.ParsePublicKey(b)
		if err != nil {
			return nil, fmt.Errorf("CP_EXTENSION_TRUST_KEYS: %w", err)
		}
		s.trust = append(s.trust, []byte(key))
	}
	return s, nil
}

// TrustEnabled reports whether any trust root is configured.
func (s *Service) TrustEnabled() bool { return len(s.trust) > 0 }

// Register validates, verifies and stores a manifest. Re-registering the same
// name replaces the manifest, resets state to pending and REVOKES all prior
// grants (new version ⇒ re-approval; silent carry-over is impossible).
func (s *Service) Register(ctx context.Context, actor string, raw json.RawMessage) (Extension, error) {
	if len(s.trust) == 0 {
		return Extension{}, ErrNoTrustKey
	}
	m, _, err := parseManifest(raw)
	if err != nil {
		return Extension{}, err
	}
	// Catalog + registry checks (kriter 1: unknown permission/op → reject).
	if s.catalog != nil {
		cat := s.catalog()
		for _, p := range m.Permissions {
			if !cat[p.Action] {
				return Extension{}, fmt.Errorf("%w: %s", ErrUnknownPermission, p.Action)
			}
		}
	}
	if s.knownOps != nil {
		known := s.knownOps()
		for _, op := range m.DelegatedOps {
			if !known[op] {
				return Extension{}, fmt.Errorf("%w: %s", ErrUnknownOp, op)
			}
		}
	}
	// Signature over canonical manifest bytes (fail-closed).
	canonical, err := canonicalManifest(raw)
	if err != nil {
		return Extension{}, err
	}
	verified := false
	for _, key := range s.trust {
		if err := signpkg.VerifyManifestBytes(key, canonical, m.Signature); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return Extension{}, ErrBadSignature
	}
	// API compatibility (kriter 1).
	if err := m.CheckAPICompatibility(s.apiVersion, s.cpVersion); err != nil {
		return Extension{}, err
	}

	// Replace-or-insert by unique name: prior grants are revoked below.
	var prev Extension
	if s.store != nil {
		prev, _, err = s.store.GetByName(ctx, m.Name)
	}
	newExt := Extension{
		Name:         m.Name,
		DisplayName:  strings.TrimSpace(m.DisplayName),
		Vendor:       strings.TrimSpace(m.Vendor),
		Version:      m.Version,
		APIVersion:   m.APIVersion,
		MinCPVersion: m.MinCPVersion,
		ArtifactSHA:  m.ArtifactSHA256,
		State:        "pending",
		Requested:    append([]PermRequest{}, m.Permissions...),
		DelegatedOps: append([]string{}, m.DelegatedOps...),
		Capabilities: append([]string{}, m.Capabilities...),
		RegisteredBy: actor,
	}
	if err == nil && prev.ID != "" {
		newExt.ID = prev.ID
	}
	if s.store == nil {
		return newExt, nil
	}
	saved, err := s.store.Upsert(ctx, newExt, raw)
	if err != nil {
		return Extension{}, err
	}
	// New version ⇒ previously approved permissions do NOT carry over.
	if prev.ID != "" {
		if err := s.store.RevokeAllGrants(ctx, saved.ID); err != nil {
			return Extension{}, err
		}
	}
	return s.refresh(ctx, saved.ID)
}

// ApprovePermission grants ONE requested permission to an extension. The
// action must be present in the (current) manifest request list.
func (s *Service) ApprovePermission(ctx context.Context, extID, action string) (Extension, error) {
	ext, _, err := s.store.Get(ctx, extID)
	if err != nil {
		return Extension{}, err
	}
	if ext.State == "revoked" {
		return Extension{}, fmt.Errorf("%w: eklenti revoked", ErrInvalidState)
	}
	requested := false
	risk := ""
	for _, p := range ext.Requested {
		if p.Action == action {
			requested, risk = true, p.Risk
			break
		}
	}
	if !requested {
		return Extension{}, fmt.Errorf("%w: %s", ErrPermNotRequested, action)
	}
	g, err := s.store.UpsertGrant(ctx, extID, Grant{Action: action, Risk: risk, GrantedBy: "", GrantedAt: time.Now().UTC()})
	if err != nil {
		return Extension{}, err
	}
	_ = g
	return s.refresh(ctx, extID)
}

// RevokePermission withdraws ONE granted permission.
func (s *Service) RevokePermission(ctx context.Context, extID, action string) (Extension, error) {
	if _, _, err := s.store.Get(ctx, extID); err != nil {
		return Extension{}, err
	}
	if err := s.store.RevokeGrant(ctx, extID, action); err != nil {
		return Extension{}, err
	}
	return s.refresh(ctx, extID)
}

// Revoke disables the extension entirely (all grants withdrawn).
func (s *Service) Revoke(ctx context.Context, extID string) (Extension, error) {
	ext, _, err := s.store.Get(ctx, extID)
	if err != nil {
		return Extension{}, err
	}
	if err := s.store.RevokeAllGrants(ctx, extID); err != nil {
		return Extension{}, err
	}
	ext.State = "revoked"
	saved, err := s.store.Upsert(ctx, ext, ext.Manifest)
	if err != nil {
		return Extension{}, err
	}
	return s.refresh(ctx, saved.ID)
}

// HasPermission reports whether extID currently holds an ACTIVE grant for
// action (the enforcement helper for future delegation paths). Authority =
// not revoked + API-compatible + explicit active grant. Nothing else grants.
func (s *Service) HasPermission(ctx context.Context, extID, action string) (bool, error) {
	ext, _, err := s.store.Get(ctx, extID)
	if err != nil {
		return false, err
	}
	if ext.State == "revoked" || !s.compatible(ext) {
		return false, nil
	}
	for _, g := range ext.Granted {
		if g.Action == action && g.RevokedAt == nil {
			return true, nil
		}
	}
	return false, nil
}

// List returns all extensions with computed compatibility.
func (s *Service) List(ctx context.Context) ([]Extension, error) {
	exts, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range exts {
		exts[i].Compatible = s.compatible(exts[i])
	}
	return exts, nil
}

// Get returns one extension with computed compatibility.
func (s *Service) Get(ctx context.Context, id string) (Extension, error) {
	return s.refresh(ctx, id)
}

func (s *Service) refresh(ctx context.Context, id string) (Extension, error) {
	ext, _, err := s.store.Get(ctx, id)
	if err != nil {
		return Extension{}, err
	}
	ext.Compatible = s.compatible(ext)
	return ext, nil
}

func (s *Service) compatible(e Extension) bool {
	if errors.Is(s.apiCompatErr(e), ErrAPIIncompatible) {
		return false
	}
	return s.apiCompatErr(e) == nil
}

func (s *Service) apiCompatErr(e Extension) error {
	m := Manifest{APIVersion: e.APIVersion, MinCPVersion: e.MinCPVersion}
	return m.CheckAPICompatibility(s.apiVersion, s.cpVersion)
}
