// Package updateops implements the Faz 15 node update adapter (ADR-0020),
// executed ONLY in the privileged broker (server-privd, --update-enable).
//
// Security model (criterion 2):
//   - The payload carries METADATA ONLY (update_id, version, sha256,
//     signature). There is deliberately NO path field: the artifact is read
//     from the node-managed incoming directory, and the swap target is the
//     node-managed server-agent binary path. The payload can never widen
//     the surface.
//   - The artifact is verified in TWO steps, both fail-closed: sha256 match
//     against the payload, then ed25519 signature (SCP-SIG-V1 canonical
//     record: name + size + sha256 — mirror of backend/internal/signpkg)
//     against the broker-configured trust key. Unsigned or mismatching
//     artifacts are REJECTED before anything is staged.
//   - Health check: the staged binary is executed with --version BEFORE the
//     swap and the swapped binary again AFTER; a failing health check
//     automatically restores the rollback copy (typed error, honest result).
//   - Activation of the new binary is the operator/service manager restart
//     (documented); update.rollback restores the previous binary at any time.
package updateops

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Operation names (mirror backend/internal/tasks ops_faz15.go).
const (
	OpUpdateStage    = "update.stage"
	OpUpdateStatus   = "update.status"
	OpUpdateRollback = "update.rollback"
)

// Node-managed paths (never payload-supplied).
const (
	IncomingDir = "/var/lib/server-control/updates/incoming"
	StagedDir   = "/var/lib/server-control/updates/staged"
	RollbackDir = "/var/lib/server-control/updates/rollback"
	TargetBin   = "/usr/local/bin/server-agent"
)

// MaxPayloadBytes mirrors the CP registry limit for update.stage.
const MaxPayloadBytes = 2048

// Stable errors (mapped to broker categories).
var (
	ErrTrustKeyMissing = errors.New("güven kökü yapılandırılmamış (--update-trust-key)")
	ErrHashMismatch    = errors.New("sha256 doğrulaması başarısız — artifact reddedildi")
	ErrBadSignature    = errors.New("imza doğrulaması başarısız — artifact reddedildi")
	ErrNoArtifact      = errors.New("gelen dizinde artifact yok")
	ErrAmbiguousInput  = errors.New("gelen dizinde birden fazla dosya — hangisi doğrulanacak belirsiz")
	ErrBadPayload      = errors.New("payload şeması uyumsuz")
	ErrNothingStaged   = errors.New("staging'de doğrulanmış güncelleme yok")
	ErrNoRollback      = errors.New("rollback kopyası yok")
	ErrHealthCheck     = errors.New("health-check başarısız — otomatik geri alma uygulandı")
	ErrSwapFailed      = errors.New("ikilik değişimi başarısız")
	ErrUnsupportedOS   = errors.New("bu aile yalnız Linux'ta yürütülür (Windows aktivasyonu operatör prosedürüdür)")
)

var (
	uuidRE   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	verRE    = regexp.MustCompile(`^[0-9A-Za-z.+-]{5,64}$`)
	sigRE    = regexp.MustCompile(`^[A-Za-z0-9+/]{86,344}={0,2}$`)
	nameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	stagedRE = regexp.MustCompile(`^staged-`)
)

// StagePayload is the typed schema for update.stage (mirror of the CP
// validator). NOTE: no path, no target — both are node-managed.
type StagePayload struct {
	UpdateID  string `json:"update_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// ValidateStage mirrors the CP validator.
func ValidateStage(p StagePayload) error {
	if !uuidRE.MatchString(p.UpdateID) {
		return ErrBadPayload
	}
	if !verRE.MatchString(p.Version) {
		return ErrBadPayload
	}
	if !shaRE.MatchString(p.SHA256) {
		return ErrBadPayload
	}
	if !sigRE.MatchString(p.Signature) {
		return ErrBadPayload
	}
	return nil
}

// canonicalArtifact is the exact SCP-SIG-V1 byte sequence signed for an
// artifact: `scp-sign-v1\n<name>\n<size>\n<sha256hex>\n`. This MUST stay
// byte-identical to backend/control-plane/internal/signpkg (contract test
// vectors exist on both sides).
func canonicalArtifact(name string, size int64, sha256hex string) ([]byte, error) {
	if !nameRE.MatchString(name) || strings.Contains(name, "..") {
		return nil, fmt.Errorf("artifact adı geçersiz: %q", name)
	}
	var b strings.Builder
	b.WriteString("scp-sign-v1\n")
	b.WriteString(name)
	b.WriteByte('\n')
	b.WriteString(fmt.Sprintf("%d", size))
	b.WriteByte('\n')
	b.WriteString(strings.ToLower(sha256hex))
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// verifyArtifact enforces sha256 + signature for one file (fail-closed).
func verifyArtifact(path string, trust []ed25519.PublicKey, p StagePayload) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("artifact okunamadı: %w", err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if digest != p.SHA256 {
		return ErrHashMismatch
	}
	if len(trust) == 0 {
		return ErrTrustKeyMissing
	}
	entry, err := os.Stat(path)
	if err != nil {
		return err
	}
	canonical, err := canonicalArtifact(baseName(path), entry.Size(), digest)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature)
	if err != nil {
		return ErrBadSignature
	}
	for _, key := range trust {
		if ed25519.Verify(key, canonical, sig) {
			return nil
		}
	}
	return ErrBadSignature
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// ParseTrustKey decodes one base64 ed25519 public key (32 bytes) for
// --update-trust-key. Exported for the daemon wiring.
func ParseTrustKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("güven kökü base64 değil: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("güven kökü %d bayt; ed25519 32 bayt olmalı", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// copyFile performs a byte-exact copy with mode preservation and fsync.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = st.Mode()
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
