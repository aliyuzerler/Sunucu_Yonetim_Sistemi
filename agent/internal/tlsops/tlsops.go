// Package tlsops implements the Faz 09 node-side TLS certificate
// operations (key/CSR generation, certificate installation with a hard
// validation gate, on-disk certificate status).
//
// Security model (ADR-0013, docs/architecture/dns-ssl.md):
//   - The TLS private key is generated HERE and NEVER leaves the node:
//     web.cert.request returns ONLY the CSR (public material). The control
//     plane completes ACME against that CSR and ships back the public
//     certificate chain — private keys never traverse the broker.
//   - web.cert.install accepts ONLY public material. Before ANY file is
//     touched it validates: PEM parses, leaf not expired, leaf SAN covers
//     the domain, and the leaf public key MATCHES the on-disk private key
//     (prevents installing a certificate nobody can use).
//   - Installation is atomic: staged temp file → rollback copy of the
//     previous chain → rename → `nginx -t` syntax gate → reload ONLY after
//     the gate passes. Failure at ANY point restores the previous chain
//     (criterion 4: a failed install/renewal never breaks the live cert).
//   - Everything runs ONLY inside the privileged broker behind
//     --web-enable (same gate as the web hosting family — ADR-0012).
package tlsops

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Operation names (mirror backend/internal/tasks ops_faz09.go).
const (
	OpWebCertRequest = "web.cert.request"
	OpWebCertInstall = "web.cert.install"
	OpWebCertStatus  = "web.cert.status"
)

// Defaults and limits (mirrored in backend ops_faz09.go / agent executors).
const (
	DefaultCertDir   = "/etc/nginx/ssl/srvctl"
	DefaultNginxBin  = "nginx"
	DefaultSystemctl = "systemctl"

	KeyFile        = "privkey.pem"
	CertFile       = "fullchain.pem"
	TempSuffix     = ".new"
	RollbackSuffix = ".rollback"

	MaxPayloadBytes = 24 << 10
	MaxDomains      = 100
	MaxCSRPEMLen    = 8 << 10
	MaxCertPEMLen   = 16 << 10
	MaxCertList     = 500
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidDomain   = errors.New("tlsops: domain geçersiz")
	ErrTooManyDomains  = errors.New("tlsops: en fazla 100 SAN")
	ErrNotFound        = errors.New("tlsops: sertifika/anahtar bulunamadı")
	ErrKeyPairMismatch = errors.New("tlsops: sertifika, mevcut özel anahtarla eşleşmiyor")
	ErrCertInvalid     = errors.New("tlsops: sertifika PEM geçersiz")
	ErrCertExpired     = errors.New("tlsops: sertifika süresi dolmuş")
	ErrSyntax          = errors.New("tlsops: nginx -t doğrulaması başarısız")
	ErrUnavailable     = errors.New("tlsops: nginx kullanılamıyor")
)

// labelRE mirrors webops/ssl per-DNS-label allowlist.
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidDomain enforces lowercase FQDN shape (no trailing dot, no "..").
func ValidDomain(d string) bool {
	if d == "" || len(d) > 253 || strings.Contains(d, "..") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if !labelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// NormalizeDomains validates and deduplicates the request domain list.
// The primary domain is first and always present.
func NormalizeDomains(domain string, sans []string) ([]string, error) {
	if !ValidDomain(domain) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidDomain, domain)
	}
	seen := map[string]bool{domain: true}
	out := []string{domain}
	for _, s := range sans {
		if s == "" {
			continue
		}
		if !ValidDomain(s) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidDomain, s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > MaxDomains {
		return nil, ErrTooManyDomains
	}
	return out, nil
}

// FingerprintOf is the lowercase hex sha256 of arbitrary DER bytes
// (public keys, certificates).
func FingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// RequestResult is the web.cert.request outcome. The private key NEVER
// appears here — only the CSR and a public-key fingerprint.
type RequestResult struct {
	Domain         string   `json:"domain"`
	SANs           []string `json:"sans"`
	CSRPEM         string   `json:"csr_pem"`
	KeyFingerprint string   `json:"key_fingerprint"`
	KeyReused      bool     `json:"key_reused"`
}

// InstallResult is the web.cert.install outcome.
type InstallResult struct {
	Domain       string    `json:"domain"`
	NotAfter     time.Time `json:"not_after"`
	Fingerprint  string    `json:"fingerprint"`
	Reloaded     bool      `json:"reloaded"`
	RollbackUsed bool      `json:"rollback_used"`
}

// CertStatus is one on-disk certificate (web.cert.status).
type CertStatus struct {
	Domain      string    `json:"domain"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	KeyPresent  bool      `json:"key_present"`
	DaysLeft    int       `json:"days_left"`
}
