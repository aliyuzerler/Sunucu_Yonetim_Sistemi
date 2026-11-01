// Package certmgmt persists the Faz 09 certificate lifecycle state
// (ADR-0013, docs/architecture/dns-ssl.md).
//
// Model:
//   - The TLS private key NEVER reaches this store (it lives on the node;
//     the control plane holds the CSR — public material — from
//     web.cert.request and reuses it for renewal).
//   - Issued certificate/chain PEM is stored AES-256-GCM sealed
//     (internal/secrets) — defense in depth even for public material.
//   - Every failure is CLASSIFIED (acmex.ErrorClass) and surfaced: rate
//     limit (with Retry-After) and challenge failures are first-class UI
//     states, not silent executor errors.
//   - Effective state: "expired" is derived lazily on read AND persisted
//     by the ExpiryWatcher so both API consumers and audit see it.
package certmgmt

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// Certificate states (closed set; mirrored in UI + docs).
const (
	StatePending     = "pending"        // kayıt var, CSR var, henüz ACME değil
	StateIssuing     = "issuing"        // ACME akışı sürüyor
	StateIssued      = "issued"         // sertifika alındı ve saklandı
	StateRenewing    = "renewing"       // yenileme akışı sürüyor
	StateRenewalFail = "renewal_failed" // yenileme başarısız — CANLI SERTİFİKA DOKUNULMAMIŞ
	StateRevoked     = "revoked"
	StateExpired     = "expired"
)

// ExpiringSoonHorizon: issued sertifikalar bu süreden kısa ömürlü kaldığında
// UI "expiring_soon" rozeti gösterir (yenileme önerilir).
const ExpiringSoonHorizon = 30 * 24 * time.Hour

// Record is one managed certificate (NO private key, ever).
type Record struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"` // birincil domain
	SANs           []string  `json:"sans"`
	ProviderID     string    `json:"provider_id"`
	NodeID         string    `json:"node_id,omitempty"` // web.cert.request edilen node
	CSRPEM         string    `json:"csr_pem,omitempty"` // public material
	State          string    `json:"state"`
	NotAfter       time.Time `json:"not_after,omitempty"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastErrorClass string    `json:"last_error_class,omitempty"`
	RetryAfterSecs int       `json:"retry_after_secs,omitempty"`
	ExpiresInDays  int       `json:"expires_in_days,omitempty"`
	ExpiringSoon   bool      `json:"expiring_soon,omitempty"`
	EffectiveState string    `json:"effective_state,omitempty"`
	IssuedAt       time.Time `json:"issued_at,omitempty"`
	RevokedAt      time.Time `json:"revoked_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// EffectiveStateOf derives the read-time state: an issued record past its
// NotAfter reports "expired" without waiting for the watcher write.
func EffectiveStateOf(r Record, now time.Time) string {
	if r.State == StateIssued && !r.NotAfter.IsZero() && now.After(r.NotAfter) {
		return StateExpired
	}
	return r.State
}

// FingerprintOf is the lowercase hex sha256 of the DER leaf.
func FingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ParseLeafPEM extracts the leaf certificate from a PEM bundle.
func ParseLeafPEM(pemBytes []byte) (*x509.Certificate, error) {
	for len(pemBytes) > 0 {
		block, rest := pem.Decode(pemBytes)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		pemBytes = rest
	}
	return nil, errors.New("certmgmt: PEM zincirinde sertifika yok")
}

// DaysLeft reports whole days until notAfter (≥0). A small tolerance
// absorbs clock jitter between caller timestamps so "45 days added" reads
// back as 45, not 44.
func DaysLeft(notAfter, now time.Time) int {
	if notAfter.IsZero() {
		return 0
	}
	d := time.Until(notAfter)
	if !now.IsZero() {
		d = notAfter.Sub(now)
	}
	days := int((d + 30*time.Second) / (24 * time.Hour))
	if days < 0 {
		return 0
	}
	return days
}

var ErrCertNotFound = errors.New("certmgmt: sertifika kaydı bulunamadı")

func fmtWrap(op string, err error) error {
	return fmt.Errorf("certmgmt: %s: %w", op, err)
}
