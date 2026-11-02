// Package updates implements the Faz 15 signed update artifact registry
// (ADR-0020): Control Plane stores METADATA for signed release artifacts
// (it never distributes binaries), verifies signatures fail-closed at
// publish time and refuses revoked artifacts in task creation.
package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

// ManifestVersion is the only accepted update manifest contract version.
const ManifestVersion = "SCP-UPDATE-MANIFEST-V1"

// Artifact targets (broker/agent families + the control plane itself).
const (
	TargetAgentLinux   = "agent_linux_amd64"
	TargetAgentWindows = "agent_windows_amd64"
	TargetControlPlane = "control_plane"
)

// States.
const (
	StatePublished = "published"
	StateRevoked   = "revoked"
)

var (
	ErrNoTrustKey      = errors.New("güven kökü yapılandırılmamış (CP_UPDATE_TRUST_KEYS) — güncelleme yayını kapalı")
	ErrBadManifest     = errors.New("update manifest geçersiz")
	ErrBadSignature    = errors.New("update manifest imzası doğrulanamadı")
	ErrDuplicate       = errors.New("bu hedef+sürüm zaten yayımlanmış")
	ErrNotFound        = errors.New("update artifact bulunamadı")
	ErrRevoked         = errors.New("update artifact iptal edilmiş")
	ErrUnknownTarget   = errors.New("bilinmeyen hedef platform")
	ErrAPIIncompatible = errors.New("API sürümü uyumsuz")
)

var (
	semverRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Manifest is the signed update artifact declaration (SCP-UPDATE-MANIFEST-V1).
type Manifest struct {
	ManifestVersion string `json:"manifest_version"`
	Target          string `json:"target"`
	Version         string `json:"version"`
	FileName        string `json:"file_name"`
	SizeBytes       int64  `json:"size_bytes"`
	SHA256          string `json:"sha256"`
	MinPrev         string `json:"min_prev,omitempty"`
	APIVersion      string `json:"api_version"`
	Notes           string `json:"notes,omitempty"`
	Signature       string `json:"signature"`
}

// ValidTargets returns the accepted target platform set.
func ValidTargets() map[string]bool {
	return map[string]bool{TargetAgentLinux: true, TargetAgentWindows: true, TargetControlPlane: true}
}

// ParseManifest strictly validates the manifest fields (signature checked
// separately by the service against trust roots).
func ParseManifest(raw json.RawMessage) (*Manifest, json.RawMessage, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, nil, fmt.Errorf("%w: şema uyumsuz: %v", ErrBadManifest, err)
	}
	if m.ManifestVersion != ManifestVersion {
		return nil, nil, fmt.Errorf("%w: manifest_version %q desteklenmiyor", ErrBadManifest, m.ManifestVersion)
	}
	if !ValidTargets()[m.Target] {
		return nil, nil, fmt.Errorf("%w: %s (%s)", ErrUnknownTarget, m.Target, strings.Join(validTargetNames(), ","))
	}
	if !semverRE.MatchString(m.Version) {
		return nil, nil, fmt.Errorf("%w: version SemVer değil: %q", ErrBadManifest, m.Version)
	}
	if err := signpkg.CheckName(m.FileName); err != nil {
		return nil, nil, fmt.Errorf("%w: file_name: %v", ErrBadManifest, err)
	}
	if m.SizeBytes <= 0 || m.SizeBytes > 1<<30 {
		return nil, nil, fmt.Errorf("%w: size_bytes 1 bayt..1 GiB olmalı", ErrBadManifest)
	}
	if !sha256RE.MatchString(m.SHA256) {
		return nil, nil, fmt.Errorf("%w: sha256 64 küçük-hex olmalı", ErrBadManifest)
	}
	if m.MinPrev != "" && !semverRE.MatchString(m.MinPrev) {
		return nil, nil, fmt.Errorf("%w: min_prev SemVer değil: %q", ErrBadManifest, m.MinPrev)
	}
	if !semverRE.MatchString(strings.TrimPrefix(m.APIVersion, "v")) {
		return nil, nil, fmt.Errorf("%w: api_version SemVer değil: %q", ErrBadManifest, m.APIVersion)
	}
	if len(m.Notes) > 512 {
		return nil, nil, fmt.Errorf("%w: notes ≤512 olmalı", ErrBadManifest)
	}
	if strings.TrimSpace(m.Signature) == "" {
		return nil, nil, fmt.Errorf("%w: signature zorunlu", ErrBadManifest)
	}
	return &m, raw, nil
}

// Canonical returns the canonical JSON of the manifest WITHOUT the signature
// field — the exact bytes the ed25519 signature covers.
func (m *Manifest) Canonical(raw json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	delete(obj, "signature")
	rest, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return signpkg.CanonicalJSON(rest)
}

func validTargetNames() []string {
	return []string{TargetAgentLinux, TargetAgentWindows, TargetControlPlane}
}
