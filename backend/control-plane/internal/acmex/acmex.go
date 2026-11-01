// Package acmex wraps golang.org/x/crypto/acme for the Faz 09 certificate
// lifecycle (ADR-0013, docs/architecture/dns-ssl.md).
//
// Design:
//   - DNS-01 only: the control plane owns provider credentials, so TXT
//     records are set THROUGH the dns.Provider adapter (never via inbound
//     HTTP that would require edge exposure).
//   - TLS private keys NEVER reach the control plane: Issue() receives a
//     CSR generated on the node (web.cert.request). The control plane only
//     ever sees public-key material.
//   - Errors are CLASSIFIED, never swallowed: rate_limit (429 / rateLimited
//     problem + Retry-After), challenge (authorization failure), order
//     invalid, network — the certificate store surfaces them to the UI.
//   - Revocation is account-signed (RFC 8555 §7.6): the server-side path
//     that needs the certificate private key is not available to us BY
//     DESIGN (the key lives on the node), so revoke is a hand-rolled JWS
//     POST with the ACME account key.
package acmex

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

// ErrorClass is the stable error classification surfaced in certificate
// state rows and the UI.
type ErrorClass string

const (
	ClassNone       ErrorClass = ""
	ClassRateLimit  ErrorClass = "rate_limit"
	ClassChallenge  ErrorClass = "challenge"
	ClassOrderInval ErrorClass = "order_invalid"
	ClassCSR        ErrorClass = "csr_invalid"
	ClassNetwork    ErrorClass = "network"
	ClassACMEOther  ErrorClass = "acme_other"
)

// Classify maps an error from the issue/renew path to (class, retryAfter).
// retryAfter > 0 only for rate limits with a server-provided window.
func Classify(err error) (ErrorClass, time.Duration) {
	if err == nil {
		return ClassNone, 0
	}
	if d, ok := acme.RateLimit(err); ok {
		return ClassRateLimit, d
	}
	var authzErr *acme.AuthorizationError
	if errors.As(err, &authzErr) {
		return ClassChallenge, 0
	}
	var orderErr *acme.OrderError
	if errors.As(err, &orderErr) {
		return ClassOrderInval, 0
	}
	var acmeErr *acme.Error
	if errors.As(err, &acmeErr) {
		if acmeErr.StatusCode == http.StatusTooManyRequests {
			d := time.Duration(0)
			if ra := acmeErr.Header.Get("Retry-After"); ra != "" {
				if secs, e := time.ParseDuration(ra + "s"); e == nil {
					d = secs
				}
			}
			return ClassRateLimit, d
		}
		if strings.Contains(acmeErr.ProblemType, "rateLimited") {
			return ClassRateLimit, 0
		}
		if strings.Contains(acmeErr.ProblemType, "rejectedIdentifier") ||
			strings.Contains(acmeErr.ProblemType, "malformed") {
			return ClassCSR, 0
		}
		return ClassACMEOther, 0
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return ClassNetwork, 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassNetwork, 0
	}
	if errors.Is(err, ErrCSRInvalid) {
		return ClassCSR, 0
	}
	return ClassACMEOther, 0
}

// ErrCSRInvalid marks a CSR the ACME flow refused up front.
var ErrCSRInvalid = errors.New("acmex: CSR geçersiz")

// DNS01Setter bridges the challenge to the dns.Provider adapter: it must
// publish TXT "<value>" at "_acme-challenge.<domain>" and be idempotent.
type DNS01Setter func(ctx context.Context, domain, value string) error

// IssueResult is a successful issuance.
type IssueResult struct {
	CertDER  []byte
	ChainDER [][]byte // intermediates (leaf excluded)
	NotAfter time.Time
	Serial   string
}

// Manager is the Faz 09 ACME manager: one account key + directory.
type Manager struct {
	// Key is the ACME account key (file-backed; see LoadOrCreateAccountKey).
	Key *ecdsa.PrivateKey
	// DirectoryURL is the ACME directory (staging for tests/disposable envs).
	DirectoryURL string
	// Contact is the account contact (e.g. mailto:ops@example.com).
	Contact []string
	// HTTPClient allows timeout/injection control (tests set Transport).
	HTTPClient *http.Client
	// MaxRetry bounds x/crypto's internal 429 retry loop: the manager sets
	// RetryBackoff to fail after MaxRetry attempts instead of waiting 10s
	// per attempt (classification must surface fast to the task result).
	MaxRetry int
	// KID is the account URL after EnsureAccount (reused across flows).
	KID acme.KeyID
}

// LoadOrCreateAccountKey loads an ECDSA P-256 account key from path,
// creating it (0600) when missing. The key file lives on the control-plane
// host — NEVER in PostgreSQL (same discipline as the node CA key).
func LoadOrCreateAccountKey(path string) (*ecdsa.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "EC PRIVATE KEY" {
			return nil, fmt.Errorf("acmex: hesap anahtar dosyası PEM EC PRIVATE KEY olmalı: %s", path)
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("acmex: hesap anahtarı ayrıştırılamadı: %w", err)
		}
		return key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("acmex: hesap anahtarı üretilemedi: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, fmt.Errorf("acmex: hesap anahtarı yazılamadı: %w", err)
	}
	return key, nil
}

func (m *Manager) client() *acme.Client {
	max := m.MaxRetry
	if max <= 0 {
		max = 1 // tek deneme: sınıflandırma hızlı yüzeye çıksın
	}
	attempt := 0
	hc := m.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &acme.Client{
		Key:          m.Key,
		DirectoryURL: m.DirectoryURL,
		HTTPClient:   hc,
		KID:          m.KID,
		RetryBackoff: func(int, *http.Request, *http.Response) time.Duration {
			attempt++
			if attempt > max {
				return -1 // yeniden deneme yok: hata sınıflandırılır
			}
			return 100 * time.Millisecond
		},
	}
}

// EnsureAccount discovers the directory and registers (or fetches) the
// account, always accepting the current ToS (disposable/test deployments
// and explicit operator opt-in by pointing DirectoryURL at a CA).
func (m *Manager) EnsureAccount(ctx context.Context) error {
	cl := m.client()
	if _, err := cl.Discover(ctx); err != nil {
		return fmt.Errorf("acmex: dizin okunamadı: %w", err)
	}
	if m.KID != "" {
		return nil
	}
	acct := &acme.Account{Contact: m.Contact}
	registered, err := cl.Register(ctx, acct, acme.AcceptTOS)
	if err != nil {
		// Zaten kayıtlıysa: RFC "account exists" 200/409 — GetReg yolu
		// Location başlığından KID alır; x/crypto 200+body döner.
		return fmt.Errorf("acmex: hesap kaydı: %w", err)
	}
	if registered == nil || registered.URI == "" {
		return errors.New("acmex: hesap URI'si alınamadı")
	}
	m.KID = acme.KeyID(registered.URI)
	return nil
}

// Issue runs one full DNS-01 issuance for domains against csrDER.
// setter publishes the TXT records. Steps are logged through returned
// error classification; no retry loop here (task worker owns retries).
func (m *Manager) Issue(ctx context.Context, domains []string, csrDER []byte, setter DNS01Setter) (IssueResult, error) {
	if len(domains) == 0 {
		return IssueResult{}, ErrCSRInvalid
	}
	if err := validateCSR(domains, csrDER); err != nil {
		return IssueResult{}, err
	}
	if err := m.EnsureAccount(ctx); err != nil {
		return IssueResult{}, err
	}
	cl := m.client()
	order, err := cl.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
	if err != nil {
		return IssueResult{}, fmt.Errorf("acmex: sipariş açılamadı: %w", err)
	}
	for _, authzURL := range order.AuthzURLs {
		if err := satisfyAuthorization(ctx, cl, authzURL, setter); err != nil {
			return IssueResult{}, err
		}
	}
	ready, err := cl.WaitOrder(ctx, order.URI)
	if err != nil {
		return IssueResult{}, fmt.Errorf("acmex: sipariş hazır olmadı: %w", err)
	}
	der, _, err := cl.CreateOrderCert(ctx, ready.FinalizeURL, csrDER, true)
	if err != nil {
		return IssueResult{}, fmt.Errorf("acmex: sertifika üretilemedi: %w", err)
	}
	if len(der) == 0 {
		return IssueResult{}, errors.New("acmex: sertifika zinciri boş döndü")
	}
	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return IssueResult{}, fmt.Errorf("acmex: yaprak sertifika ayrıştırılamadı: %w", err)
	}
	return IssueResult{
		CertDER:  der[0],
		ChainDER: der[1:],
		NotAfter: leaf.NotAfter,
		Serial:   leaf.SerialNumber.Text(16),
	}, nil
}

func satisfyAuthorization(ctx context.Context, cl *acme.Client, authzURL string, setter DNS01Setter) error {
	authz, err := cl.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("acmex: yetkilendirme okunamadı: %w", err)
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	var dnsChal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "dns-01" {
			dnsChal = c
			break
		}
	}
	if dnsChal == nil {
		return fmt.Errorf("acmex: %s için dns-01 challenge sunulmadı", authz.Identifier)
	}
	record, err := cl.DNS01ChallengeRecord(dnsChal.Token)
	if err != nil {
		return fmt.Errorf("acmex: TXT değeri üretilemedi: %w", err)
	}
	if setter == nil {
		return errors.New("acmex: DNS-01 sağlayıcı köprüsü yapılandırılmadı")
	}
	if err := setter(ctx, authz.Identifier.Value, record); err != nil {
		return fmt.Errorf("acmex: DNS-01 TXT yayımlanamadı (%s): %w", authz.Identifier.Value, err)
	}
	if _, err := cl.Accept(ctx, dnsChal); err != nil {
		return fmt.Errorf("acmex: challenge kabul edilemedi: %w", err)
	}
	final, err := cl.WaitAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("acmex: yetkilendirme doğrulanamadı (%s): %w", authz.Identifier.Value, err)
	}
	_ = final
	return nil
}

// Revoke revokes certDER with the ACCOUNT key (RFC 8555 §7.6, "account
// key pair" path) — the certificate private key is intentionally not
// available here (key-on-node invariant, ADR-0013).
// reason: RFC 5280 CRLReason codes 0..10.
func (m *Manager) Revoke(ctx context.Context, certDER []byte, reason int) error {
	if reason < 0 || reason > 10 {
		return fmt.Errorf("acmex: iptal gerekçesi 0..10 olmalı: %d", reason)
	}
	cl := m.client()
	dir, err := cl.Discover(ctx)
	if err != nil {
		return fmt.Errorf("acmex: dizin okunamadı: %w", err)
	}
	if m.KID == "" {
		if err := m.EnsureAccount(ctx); err != nil {
			return err
		}
	}
	nonce, err := m.fetchNonce(ctx, dir.NonceURL)
	if err != nil {
		return err
	}
	return m.postJWSRevoke(ctx, dir.RevokeURL, string(m.KID), nonce, certDER, reason)
}

func (m *Manager) fetchNonce(ctx context.Context, nonceURL string) (string, error) {
	hc := m.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nonceURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("acmex: nonce alınamadı: %w", err)
	}
	defer resp.Body.Close()
	nonce := resp.Header.Get("Replay-Nonce")
	if nonce == "" {
		return "", errors.New("acmex: sunucu Replay-Nonce başlığı vermedi")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nonce, nil
}

// b64url is raw (unpadded) base64url per RFC 7515.
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (m *Manager) postJWSRevoke(ctx context.Context, revokeURL, kid, nonce string, certDER []byte, reason int) error {
	protected, err := json.Marshal(map[string]any{
		"alg":   "ES256",
		"nonce": nonce,
		"url":   revokeURL,
		"kid":   kid,
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"certificate": b64url(certDER),
		"reason":      reason,
	})
	if err != nil {
		return err
	}
	signingInput := b64url(protected) + "." + b64url(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := ecdsa.SignASN1(rand.Reader, m.Key, digest[:])
	if err != nil {
		return fmt.Errorf("acmex: JWS imzalanamadı: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"protected": b64url(protected),
		"payload":   b64url(payload),
		"signature": b64url(sig),
	})
	if err != nil {
		return err
	}
	hc := m.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("acmex: iptal isteği başarısız: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case strings.Contains(string(raw), "alreadyRevoked"):
		return nil // idempotent: zaten iptal edilmiş
	default:
		var acmeErr acme.Error
		_ = json.Unmarshal(raw, &acmeErr)
		acmeErr.StatusCode = resp.StatusCode
		if acmeErr.ProblemType == "" {
			acmeErr.ProblemType = "urn:servercontrol:revoke_failed"
			acmeErr.Detail = strings.TrimSpace(string(raw))
		}
		return &acmeErr
	}
}

// validateCSR up-front checks the CSR covers every requested domain
// (fail fast before any network traffic).
func validateCSR(domains []string, csrDER []byte) error {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return fmt.Errorf("%w: ayrıştırma: %v", ErrCSRInvalid, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("%w: imza: %v", ErrCSRInvalid, err)
	}
	want := map[string]bool{}
	for _, d := range domains {
		want[strings.ToLower(d)] = true
	}
	for _, n := range csr.DNSNames {
		delete(want, strings.ToLower(n))
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for d := range want {
			missing = append(missing, d)
		}
		return fmt.Errorf("%w: CSR SAN alanları eksik: %v", ErrCSRInvalid, missing)
	}
	return nil
}

var _ crypto.Signer = (*ecdsa.PrivateKey)(nil)
