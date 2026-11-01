package certmgmt

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/acmex"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dns"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

// Service orchestrates the ACME certificate lifecycle on top of the
// certificate store and the DNS provider bridge (ADR-0013):
//   - TLS private keys stay on the node; Issue() consumes a CSR.
//   - DNS-01 TXT records are published THROUGH dns.Service (the same
//     encrypted-credential path as record operations).
//   - Failures are classified and PERSISTED on the record (rate limit with
//     Retry-After, challenge, ...) before being returned, so the UI sees
//     them even without reading the task attempt.
type Service struct {
	Store   *Store
	DNS     *dns.Service
	ACME    *acmex.Manager
	Secrets secrets.Key
}

var (
	ErrACMEUnconfigured = errors.New("certmgmt: ACME yöneticisi yapılandırılmadı (CP_ACME_DIRECTORY_URL + CP_ACME_ACCOUNT_KEY)")
	ErrDNSUnconfigured  = errors.New("certmgmt: DNS köprüsü yapılandırılmadı")
	ErrKeyMissing       = errors.New("certmgmt: şifreleme anahtarı yapılandırılmadı (CP_SECRETS_KEY)")
)

// IssueRequest is one issuance/renewal input.
type IssueRequest struct {
	CertID     string // dolu → mevcut kayıt (yenileme); boş → yeni kayıt
	Name       string
	SANs       []string
	ProviderID string
	NodeID     string
	CSRPEM     string
}

// IssueOutcome is a successful issuance/renewal: the persisted record PLUS
// the public PEM bundle. The PEM is PUBLIC material — it is returned so the
// web.cert.install task can be chained without a decrypt round-trip; the
// private key never participates (it lives on the node).
type IssueOutcome struct {
	Record   Record
	CertPEM  string
	ChainPEM string
}

// Issue runs one full ACME DNS-01 issuance and persists the result.
func (s *Service) Issue(ctx context.Context, req IssueRequest) (IssueOutcome, error) {
	if s.ACME == nil {
		return IssueOutcome{}, ErrACMEUnconfigured
	}
	if s.DNS == nil {
		return IssueOutcome{}, ErrDNSUnconfigured
	}
	var zero secrets.Key
	if s.Secrets == zero {
		return IssueOutcome{}, ErrKeyMissing
	}
	allDomains, csrDER, err := parseCSR(req)
	if err != nil {
		return IssueOutcome{}, err
	}

	// Kayıt: yeni (pending) veya mevcut (renewing).
	var rec Record
	if req.CertID != "" {
		rec, err = s.Store.Get(ctx, req.CertID)
		if err != nil {
			return IssueOutcome{}, err
		}
		if err := s.Store.MarkRenewing(ctx, rec.ID); err != nil {
			return IssueOutcome{}, err
		}
	} else {
		rec, err = s.Store.Create(ctx, Record{
			Name: req.Name, SANs: allDomains, ProviderID: req.ProviderID,
			NodeID: req.NodeID, CSRPEM: req.CSRPEM,
		})
		if err != nil {
			return IssueOutcome{}, err
		}
	}

	// issuing'e geç: FRESH akışta pending→issuing ZORUNLUDUR — geçiş
	// başarısızsa ACME'ye hiç başlanmaz (hızlı, dürüst hata). RENEWAL'da
	// kayıt zaten renewing durumundadır ve MarkIssuing yalnız
	// pending/renewal_failed/expired'tan kabul ettiği için reddi bilinçli
	// no-op'tur (IT-03: davranış aynı, niyet artık açık).
	if err := s.Store.MarkIssuing(ctx, rec.ID); err != nil {
		if req.CertID == "" {
			return IssueOutcome{}, err
		}
		// renewal: beklenen red — renewing zaten akış durumu.
	}

	providerID := req.ProviderID
	if providerID == "" {
		providerID = rec.ProviderID
	}
	result, issueErr := s.ACME.Issue(ctx, allDomains, csrDER, s.dnsSetter(providerID))
	if issueErr != nil {
		cls, retry := acmex.Classify(issueErr)
		failState := StatePending
		if req.CertID != "" {
			failState = StateRenewalFail
		}
		_ = s.Store.MarkFailed(ctx, rec.ID, failState, string(cls), issueErr.Error(), int(retry.Seconds()))
		return IssueOutcome{}, fmt.Errorf("certmgmt: üretim başarısız (%s): %w", cls, issueErr)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: result.CertDER})
	chainPEM := []byte{}
	for _, der := range result.ChainDER {
		chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	certEnc, err := s.Secrets.Encrypt(certPEM)
	if err != nil {
		return IssueOutcome{}, fmt.Errorf("certmgmt: sertifika şifrelenemedi: %w", err)
	}
	chainEnc, err := s.Secrets.Encrypt(chainPEM)
	if err != nil {
		return IssueOutcome{}, fmt.Errorf("certmgmt: zincir şifrelenemedi: %w", err)
	}
	if err := s.Store.MarkIssued(ctx, rec.ID, certEnc, chainEnc, result.NotAfter, FingerprintOf(result.CertDER)); err != nil {
		return IssueOutcome{}, err
	}
	final, err := s.Store.Get(ctx, rec.ID)
	if err != nil {
		return IssueOutcome{}, err
	}
	return IssueOutcome{Record: final, CertPEM: string(certPEM), ChainPEM: string(chainPEM)}, nil
}

// dnsSetter bridges the ACME manager to the DNS provider adapter.
func (s *Service) dnsSetter(providerID string) acmex.DNS01Setter {
	return func(ctx context.Context, domain, value string) error {
		return s.DNS.SetTXTRecord(ctx, providerID, domain, value)
	}
}

// Renew re-issues using the STORED CSR (same key pair — documented
// tradeoff in ADR-0013; key rotation is future work). Failure leaves the
// issued material and the node's live certificate untouched.
func (s *Service) Renew(ctx context.Context, certID string) (IssueOutcome, error) {
	rec, err := s.Store.Get(ctx, certID)
	if err != nil {
		return IssueOutcome{}, err
	}
	if rec.State != StateIssued && EffectiveStateOf(rec, time.Now()) != StateExpired &&
		rec.State != StateRenewalFail {
		return IssueOutcome{}, fmt.Errorf("certmgmt: yalnız issued/renewal_failed/expired kayıt yenilenir (mevcut: %s)", rec.State)
	}
	return s.Issue(ctx, IssueRequest{
		CertID: rec.ID, Name: rec.Name, SANs: rec.SANs,
		ProviderID: rec.ProviderID, NodeID: rec.NodeID, CSRPEM: rec.CSRPEM,
	})
}

// RevokeReasonBounds: RFC 5280 CRL reason codes accepted by the flow.
func ValidRevokeReason(r int) bool { return r >= 0 && r <= 10 }

// Revoke revokes the issued certificate via the ACCOUNT key path; the
// node's key is not needed and not requested (key-on-node invariant).
func (s *Service) Revoke(ctx context.Context, certID string, reason int) (Record, error) {
	if s.ACME == nil {
		return Record{}, ErrACMEUnconfigured
	}
	var zero secrets.Key
	if s.Secrets == zero {
		return Record{}, ErrKeyMissing
	}
	rec, err := s.Store.Get(ctx, certID)
	if err != nil {
		return Record{}, err
	}
	if rec.State == StateRevoked {
		return rec, nil // idempotent
	}
	certEnc, _, err := s.Store.GetCertMaterial(ctx, certID)
	if err != nil {
		return Record{}, fmt.Errorf("certmgmt: iptal için sertifika materyali yok: %w", err)
	}
	certPEM, err := s.Secrets.Decrypt(certEnc)
	if err != nil {
		return Record{}, fmt.Errorf("certmgmt: sertifika çözülemedi: %w", err)
	}
	leaf, err := ParseLeafPEM(certPEM)
	if err != nil {
		return Record{}, err
	}
	if err := s.ACME.Revoke(ctx, leaf.Raw, reason); err != nil {
		cls, _ := acmex.Classify(err)
		// Revocation failure does NOT corrupt the record's issued state;
		// classification is surfaced in the task result.
		return Record{}, fmt.Errorf("certmgmt: iptal başarısız (%s): %w", cls, err)
	}
	if err := s.Store.MarkRevoked(ctx, certID); err != nil {
		return Record{}, err
	}
	return s.Store.Get(ctx, certID)
}

// parseCSR validates the request shape and decodes the PEM CSR.
func parseCSR(req IssueRequest) ([]string, []byte, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" || len(name) > 253 {
		return nil, nil, fmt.Errorf("%w: birincil domain geçersiz", acmex.ErrCSRInvalid)
	}
	domains := []string{name}
	seen := map[string]bool{name: true}
	for _, s := range req.SANs {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if len(s) > 253 {
			return nil, nil, fmt.Errorf("%w: SAN çok uzun", acmex.ErrCSRInvalid)
		}
		if !seen[s] {
			seen[s] = true
			domains = append(domains, s)
		}
	}
	if len(domains) > 100 {
		return nil, nil, fmt.Errorf("%w: en fazla 100 SAN", acmex.ErrCSRInvalid)
	}
	block, _ := pem.Decode([]byte(req.CSRPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, nil, fmt.Errorf("%w: csr_pem CERTIFICATE REQUEST PEM olmalı", acmex.ErrCSRInvalid)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", acmex.ErrCSRInvalid, err)
	}
	_ = csr
	return domains, block.Bytes, nil
}
