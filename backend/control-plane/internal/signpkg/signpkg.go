// Package signpkg implements the SCP-SIG-V1 signature scheme shared by
// release artifacts (ADR-0020) and extension manifests (ADR-0019): ed25519
// signatures over canonical byte sequences, standard library only.
//
// Artifact signatures cover `scp-sign-v1\n<name>\n<size>\n<sha256hex>\n`
// so the file name, size and content digest are all bound to one signature.
// Manifest signatures cover the canonical JSON encoding of the manifest
// without the signature field (map-key sorted, numbers preserved).
package signpkg

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Domain separator for artifact signatures (ADR-0020).
const artifactDomain = "scp-sign-v1"

// ErrBadSignature is returned when signature verification fails.
var ErrBadSignature = errors.New("imza doğrulanamadı")

// ErrBadName rejects artifact names that are not safe single path components.
var ErrBadName = errors.New("artifact adı geçersiz")

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// CheckName enforces a safe artifact/file name (no separators, no traversal).
func CheckName(name string) error {
	if !nameRE.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("%w: %q", ErrBadName, name)
	}
	return nil
}

// SHA256Hex returns the lowercase hex sha256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalArtifact builds the exact byte sequence signed for an artifact.
func canonicalArtifact(name string, size int64, sha256hex string) ([]byte, error) {
	if err := CheckName(name); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, errors.New("artifact boyutu negatif olamaz")
	}
	if len(sha256hex) != 64 {
		return nil, errors.New("sha256 64 hex karakter olmalı")
	}
	if _, err := hex.DecodeString(sha256hex); err != nil {
		return nil, errors.New("sha256 hex kodlanmış olmalı")
	}
	var b strings.Builder
	b.WriteString(artifactDomain)
	b.WriteByte('\n')
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("%d", size))
	b.WriteByte('\n')
	b.WriteString(strings.ToLower(sha256hex))
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// SignArtifact computes sha256(data) and signs the canonical artifact record.
// It returns the base64 signature and the lowercase hex digest.
func SignArtifact(priv ed25519.PrivateKey, name string, data []byte) (sigB64, sha256hex string, err error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", "", errors.New("ed25519 özel anahtarı geçersiz")
	}
	digest := SHA256Hex(data)
	canonical, err := canonicalArtifact(name, int64(len(data)), digest)
	if err != nil {
		return "", "", err
	}
	sig := ed25519.Sign(priv, canonical)
	return base64.StdEncoding.EncodeToString(sig), digest, nil
}

// VerifyArtifact verifies an artifact signature against the canonical record.
// Any mismatch of name/size/digest fails closed.
func VerifyArtifact(pub ed25519.PublicKey, name string, size int64, sha256hex, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("ed25519 genel anahtarı geçersiz")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("imza base64 değil: %w", err)
	}
	canonical, err := canonicalArtifact(name, size, sha256hex)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, canonical, sig) {
		return ErrBadSignature
	}
	return nil
}

// CanonicalJSON returns a deterministic JSON encoding of raw: object keys are
// sorted (Go map marshalling), whitespace removed, numbers preserved exactly
// via json.Number. Manifest signatures are computed over this encoding.
func CanonicalJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("JSON çözülemedi: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("kanonik yazım başarısız: %w", err)
	}
	return out, nil
}

// VerifyManifestBytes verifies an ed25519 signature over canonical JSON bytes.
func VerifyManifestBytes(pub ed25519.PublicKey, canonical []byte, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("ed25519 genel anahtarı geçersiz")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("imza base64 değil: %w", err)
	}
	if !ed25519.Verify(pub, canonical, sig) {
		return ErrBadSignature
	}
	return nil
}

// ParsePublicKey decodes a base64 stdlib ed25519 public key (32 bytes).
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("genel anahtar base64 değil: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("genel anahtar %d bayt; ed25519 32 bayt olmalı", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}
