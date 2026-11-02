// Package extensions implements the Faz 15 extension contract (ADR-0019):
// signed manifests (SCP-EXT-MANIFEST-V1), NO default permissions (every
// permission is explicitly granted by an administrator), API compatibility
// checks and typed delegation to EXISTING registered task operations only.
// An extension never executes code in the control plane.
package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/signpkg"
)

// ManifestVersion is the only accepted manifest contract version.
const ManifestVersion = "SCP-EXT-MANIFEST-V1"

// Typed errors (mapped to HTTP codes in the handler layer).
var (
	ErrBadManifest        = errors.New("manifest geçersiz")
	ErrBadSignature       = errors.New("manifest imzası doğrulanamadı")
	ErrNoTrustKey         = errors.New("güven kökü yapılandırılmamış (CP_EXTENSION_TRUST_KEYS) — eklenti kaydı kapalı")
	ErrAPIIncompatible    = errors.New("API sürümü uyumsuz")
	ErrDuplicate          = errors.New("eklenti adı zaten kayıtlı")
	ErrUnknownPermission  = errors.New("bilinmeyen izin")
	ErrUnknownOp          = errors.New("bilinmeyen operasyon")
	ErrNotFound           = errors.New("eklenti bulunamadı")
	ErrPermNotRequested   = errors.New("izin manifestte istenmemiş")
	ErrInvalidState       = errors.New("geçersiz durum geçişi")
	ErrUnknownTrustPolicy = errors.New("güven politikası ihlali")
)

var (
	extNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	permRE    = regexp.MustCompile(`^[a-z]+:[a-z]+$`)
	semverRE  = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	sha256RE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	maxPerms  = 32
	maxOps    = 32
	maxCaps   = 32
)

// PermRequest is one permission the manifest ASKS for. Nothing is granted by
// registration; an administrator must approve each entry (kriter 1).
type PermRequest struct {
	Action string `json:"action"`
	Risk   string `json:"risk"`
	Reason string `json:"reason"`
}

// Manifest is the signed extension declaration (ADR-0019).
type Manifest struct {
	ManifestVersion string        `json:"manifest_version"`
	Name            string        `json:"name"`
	DisplayName     string        `json:"display_name"`
	Vendor          string        `json:"vendor"`
	Version         string        `json:"version"`
	APIVersion      string        `json:"api_version"`
	MinCPVersion    string        `json:"min_cp_version"`
	ArtifactSHA256  string        `json:"artifact_sha256"`
	Permissions     []PermRequest `json:"permissions"`
	DelegatedOps    []string      `json:"delegated_ops"`
	Capabilities    []string      `json:"capabilities"`
	Signature       string        `json:"signature"`
}

// parseManifest strictly decodes and validates a manifest JSON document
// (without evaluating the signature). Unknown fields are rejected.
func parseManifest(raw json.RawMessage) (*Manifest, json.RawMessage, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, nil, fmt.Errorf("%w: şema uyumsuz: %v", ErrBadManifest, err)
	}
	if m.ManifestVersion != ManifestVersion {
		return nil, nil, fmt.Errorf("%w: manifest_version %q desteklenmiyor (%s)", ErrBadManifest, m.ManifestVersion, ManifestVersion)
	}
	if !extNameRE.MatchString(m.Name) {
		return nil, nil, fmt.Errorf("%w: name %q biçimi geçersiz", ErrBadManifest, m.Name)
	}
	if l := len(strings.TrimSpace(m.DisplayName)); l < 1 || l > 128 {
		return nil, nil, fmt.Errorf("%w: display_name 1..128 olmalı", ErrBadManifest)
	}
	if l := len(strings.TrimSpace(m.Vendor)); l < 1 || l > 128 {
		return nil, nil, fmt.Errorf("%w: vendor 1..128 olmalı", ErrBadManifest)
	}
	if !semverRE.MatchString(m.Version) {
		return nil, nil, fmt.Errorf("%w: version SemVer değil: %q", ErrBadManifest, m.Version)
	}
	if _, err := ParseAPIRange(m.APIVersion); err != nil {
		return nil, nil, err
	}
	if !semverRE.MatchString(m.MinCPVersion) {
		return nil, nil, fmt.Errorf("%w: min_cp_version SemVer değil: %q", ErrBadManifest, m.MinCPVersion)
	}
	if !sha256RE.MatchString(m.ArtifactSHA256) {
		return nil, nil, fmt.Errorf("%w: artifact_sha256 64 küçük-hex olmalı", ErrBadManifest)
	}
	if len(m.Permissions) > maxPerms {
		return nil, nil, fmt.Errorf("%w: permissions > %d", ErrBadManifest, maxPerms)
	}
	seen := map[string]bool{}
	for i, p := range m.Permissions {
		if !permRE.MatchString(p.Action) {
			return nil, nil, fmt.Errorf("%w: permissions[%d].action biçimi geçersiz: %q", ErrBadManifest, i, p.Action)
		}
		switch p.Risk {
		case "low", "medium", "high":
		default:
			return nil, nil, fmt.Errorf("%w: permissions[%d].risk low|medium|high olmalı", ErrBadManifest, i)
		}
		if l := len(strings.TrimSpace(p.Reason)); l < 10 || l > 512 {
			return nil, nil, fmt.Errorf("%w: permissions[%d].reason 10..512 olmalı", ErrBadManifest, i)
		}
		if seen[p.Action] {
			return nil, nil, fmt.Errorf("%w: permissions[%d] tekrarlı: %s", ErrBadManifest, i, p.Action)
		}
		seen[p.Action] = true
	}
	if len(m.DelegatedOps) > maxOps {
		return nil, nil, fmt.Errorf("%w: delegated_ops > %d", ErrBadManifest, maxOps)
	}
	seenOp := map[string]bool{}
	for i, op := range m.DelegatedOps {
		if len(op) < 3 || len(op) > 128 {
			return nil, nil, fmt.Errorf("%w: delegated_ops[%d] uzunluğu geçersiz", ErrBadManifest, i)
		}
		if seenOp[op] {
			return nil, nil, fmt.Errorf("%w: delegated_ops[%d] tekrarlı", ErrBadManifest, i)
		}
		seenOp[op] = true
	}
	if len(m.Capabilities) > maxCaps {
		return nil, nil, fmt.Errorf("%w: capabilities > %d", ErrBadManifest, maxCaps)
	}
	if strings.TrimSpace(m.Signature) == "" {
		return nil, nil, fmt.Errorf("%w: signature zorunlu", ErrBadManifest)
	}
	return &m, raw, nil
}

// canonicalManifest returns the canonical JSON of the manifest WITHOUT the
// signature field — the exact bytes the ed25519 signature must cover.
func canonicalManifest(raw json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("%w: şema uyumsuz: %v", ErrBadManifest, err)
	}
	if _, ok := obj["signature"]; !ok {
		return nil, fmt.Errorf("%w: signature alanı yok", ErrBadManifest)
	}
	delete(obj, "signature")
	rest, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return signpkg.CanonicalJSON(rest)
}

// ---- Minimal SemVer (stdlib-only) ----

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) (semver, error) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return semver{}, fmt.Errorf("SemVer değil: %q", s)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return semver{major: maj, minor: min, patch: pat, pre: m[4]}, nil
}

// cmpSemver compares a and b per SemVer 2.0.0 precedence.
func cmpSemver(a, b semver) int {
	if a.major != b.major {
		return sign(a.major - b.major)
	}
	if a.minor != b.minor {
		return sign(a.minor - b.minor)
	}
	if a.patch != b.patch {
		return sign(a.patch - b.patch)
	}
	// Precedence: release > pre-release.
	switch {
	case a.pre == "" && b.pre == "":
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	}
	return cmpPre(a.pre, b.pre)
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

func cmpPre(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil:
			return -1 // numeric < alphanumeric
		case berr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	return sign(len(as) - len(bs))
}

// ---- API compatibility range (kriter 1) ----

type constraint struct {
	op string
	v  semver
}

// APIRange is a parsed conjunction (AND) of version constraints.
type APIRange struct{ cs []constraint }

// ParseAPIRange parses constraints like ">=1.0.0", ">=1.0.0 <2.0.0", "^1.2.0".
// Ops: >=, >, <=, <, =, ^ (same major, at least v). At least one required.
func ParseAPIRange(s string) (APIRange, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 || len(fields) > 4 {
		return APIRange{}, fmt.Errorf("%w: api_version aralığı 1..4 kısıt olmalı: %q", ErrBadManifest, s)
	}
	var r APIRange
	for _, f := range fields {
		op := "="
		rest := f
		switch {
		case strings.HasPrefix(f, ">="), strings.HasPrefix(f, "<="):
			op, rest = f[:2], f[2:]
		case strings.HasPrefix(f, ">"), strings.HasPrefix(f, "<"):
			op, rest = f[:1], f[1:]
		case strings.HasPrefix(f, "^"):
			op, rest = "^", f[1:]
		case strings.HasPrefix(f, "="):
			op, rest = "=", f[1:]
		}
		v, err := parseSemver(rest)
		if err != nil {
			return APIRange{}, fmt.Errorf("%w: api_version kısıtı çözülemedi: %q", ErrBadManifest, f)
		}
		r.cs = append(r.cs, constraint{op: op, v: v})
	}
	return r, nil
}

// Matches reports whether v satisfies every constraint.
func (r APIRange) Matches(v semver) bool {
	for _, c := range r.cs {
		cmp := cmpSemver(v, c.v)
		ok := false
		switch c.op {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "=":
			ok = cmp == 0
		case "^":
			ok = cmp >= 0 && v.major == c.v.major
		}
		if !ok {
			return false
		}
	}
	return true
}

// CheckAPICompatibility verifies the manifest range intersects the running
// control plane (apiVersion) and honors min_cp_version (kriter 1).
func (m *Manifest) CheckAPICompatibility(apiVersion, cpVersion string) error {
	rng, err := ParseAPIRange(m.APIVersion)
	if err != nil {
		return err
	}
	av, err := parseSemver(apiVersion)
	if err != nil {
		return fmt.Errorf("CP api_version çözülemedi: %w", err)
	}
	if !rng.Matches(av) {
		return fmt.Errorf("%w: manifest %q aralığı CP API %s ile kesişmiyor", ErrAPIIncompatible, m.APIVersion, apiVersion)
	}
	mv, err := parseSemver(m.MinCPVersion)
	if err != nil {
		return err
	}
	cv, err := parseSemver(cpVersion)
	if err != nil {
		return fmt.Errorf("CP sürümü çözülemedi: %w", err)
	}
	if cmpSemver(cv, mv) < 0 {
		return fmt.Errorf("%w: CP %s < manifest min_cp_version %s", ErrAPIIncompatible, cpVersion, m.MinCPVersion)
	}
	return nil
}
