package updates

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

// Artifact is the stored metadata record for one signed release artifact.
type Artifact struct {
	ID          string          `json:"id"`
	Target      string          `json:"target"`
	Version     string          `json:"version"`
	FileName    string          `json:"file_name"`
	SizeBytes   int64           `json:"size_bytes"`
	SHA256      string          `json:"sha256"`
	Signature   string          `json:"signature"`
	MinPrev     string          `json:"min_prev,omitempty"`
	APIVersion  string          `json:"api_version"`
	Notes       string          `json:"notes,omitempty"`
	State       string          `json:"state"`
	Manifest    json.RawMessage `json:"manifest"`
	PublishedBy string          `json:"published_by"`
	PublishedAt time.Time       `json:"published_at"`
}

// Store persists update artifact metadata.
type Store interface {
	Insert(ctx context.Context, a Artifact, manifest json.RawMessage) (Artifact, error)
	Get(ctx context.Context, id string) (Artifact, error)
	GetByVersion(ctx context.Context, target, version string) (Artifact, error)
	List(ctx context.Context, target string) ([]Artifact, error)
	SetState(ctx context.Context, id, state string) error
}

// Service publishes and revokes update artifact metadata. Signature
// verification is fail-closed: without a configured trust root NOTHING can
// be published (ADR-0020; threat model release blocker).
type Service struct {
	store Store
	trust []ed25519.PublicKey
}

// NewService builds the service; trustB64 entries are base64 ed25519 public
// keys. Invalid entries abort construction.
func NewService(store Store, trustB64 []string) (*Service, error) {
	s := &Service{store: store}
	for _, b := range trustB64 {
		if b == "" {
			continue
		}
		key, err := signpkg.ParsePublicKey(b)
		if err != nil {
			return nil, err
		}
		s.trust = append(s.trust, key)
	}
	return s, nil
}

// TrustEnabled reports whether publishing is possible at all.
func (s *Service) TrustEnabled() bool { return len(s.trust) > 0 }

// Publish validates + verifies + stores one update manifest.
func (s *Service) Publish(ctx context.Context, actor string, raw json.RawMessage) (Artifact, error) {
	if len(s.trust) == 0 {
		return Artifact{}, ErrNoTrustKey
	}
	m, _, err := ParseManifest(raw)
	if err != nil {
		return Artifact{}, err
	}
	canonical, err := m.Canonical(raw)
	if err != nil {
		return Artifact{}, err
	}
	verified := false
	for _, key := range s.trust {
		if err := signpkg.VerifyManifestBytes(key, canonical, m.Signature); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return Artifact{}, ErrBadSignature
	}
	if _, err := s.store.GetByVersion(ctx, m.Target, m.Version); err == nil {
		return Artifact{}, ErrDuplicate
	}
	a := Artifact{
		Target:      m.Target,
		Version:     m.Version,
		FileName:    m.FileName,
		SizeBytes:   m.SizeBytes,
		SHA256:      m.SHA256,
		Signature:   m.Signature,
		MinPrev:     m.MinPrev,
		APIVersion:  m.APIVersion,
		Notes:       m.Notes,
		State:       StatePublished,
		PublishedBy: actor,
	}
	return s.store.Insert(ctx, a, raw)
}

// Revoke marks an artifact revoked — task creation refuses it afterwards.
func (s *Service) Revoke(ctx context.Context, id string) error {
	if _, err := s.store.Get(ctx, id); err != nil {
		return err
	}
	return s.store.SetState(ctx, id, StateRevoked)
}

// Stageable checks the task-creation contract for update.stage: the artifact
// must exist, be published (never revoked) and match the payload metadata.
func (s *Service) Stageable(ctx context.Context, id, version, sha256hex, signature string) error {
	a, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if a.State != StatePublished {
		return ErrRevoked
	}
	if a.Version != version || a.SHA256 != sha256hex || a.Signature != signature {
		return ErrBadManifest
	}
	return nil
}

// List returns artifacts (target optional filter).
func (s *Service) List(ctx context.Context, target string) ([]Artifact, error) {
	if target != "" && !ValidTargets()[target] {
		return nil, ErrUnknownTarget
	}
	return s.store.List(ctx, target)
}
