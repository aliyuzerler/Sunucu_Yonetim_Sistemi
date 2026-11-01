package certmgmt

// Uçtan uca sertifikasyon akışı: fake ACME sunucusu + mock DNS sağlayıcı
// + PostgreSQL durumu. TEST_DATABASE_URL gerekmez kısmı (parseCSR) ayrı
// birim testindedir; akış testi testdb.Pool ile atlanabilir koşar.
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/acmex"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/acmex/fakeca"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dns"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

func testCSRPEM(t *testing.T, domains []string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))
}

func newFlow(t *testing.T) (*Service, *fakeca.Server, *dns.Service) {
	t.Helper()
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)

	f := fakeca.New().Start()
	t.Cleanup(f.Close)

	key, err := secrets.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dnsSvc := &dns.Service{Key: key}
	mgr := &acmex.Manager{
		DirectoryURL: f.DirURL(),
		Contact:      []string{"mailto:ops@example.internal"},
		MaxRetry:     1,
	}
	// ACME hesap anahtarı: test anahtarı (dosya yok — doğrudan enjekte).
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mgr.Key = ek
	f.VerifyDNS01 = func(domain, token string) error {
		// DNS-01 challenge doğrulaması: akış testinde TXT varlığı mock
		// sağlayıcı üzerinden ayrıca kanıtlanır (TestFlowIssueRenewRevoke).
		_ = domain
		_ = token
		return nil
	}
	svc := &Service{Store: NewStore(pool), DNS: dnsSvc, ACME: mgr, Secrets: key}
	return svc, f, dnsSvc
}

func TestFlowIssueRenewRevoke(t *testing.T) {
	svc, _, dnsSvc := newFlow(t)
	ctx := t.Context()

	// DNS sağlayıcı: mock, kayıtlı.
	if _, err := dnsSvc.CreateProvider(ctx, "flow-provider", "mock", json.RawMessage(`{"api_key":"flow-key-1"}`)); err != nil {
		t.Fatalf("provider: %v", err)
	}
	providers, err := dnsSvc.ListProviders(ctx)
	if err != nil || len(providers) != 1 {
		t.Fatalf("providers: %v %v", providers, err)
	}
	providerID := providers[0].ID

	// 1) Issue: CSR (node'da üretilmiş gibi) + ACME akışı.
	csr := testCSRPEM(t, []string{"flow.example.internal"})
	out, err := svc.Issue(ctx, IssueRequest{
		Name: "flow.example.internal", ProviderID: providerID,
		NodeID: "node-flow", CSRPEM: csr,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	rec := out.Record
	if rec.State != StateIssued {
		t.Fatalf("durum: %s", rec.State)
	}
	// Public PEM sonuçta taşınır (install zinciri); özel anahtar ASLA yok.
	if !strings.Contains(out.CertPEM, "BEGIN CERTIFICATE") {
		t.Fatal("IssueOutcome.CertPEM public PEM olmalı")
	}
	if strings.Contains(out.CertPEM, "PRIVATE KEY") || strings.Contains(out.ChainPEM, "PRIVATE KEY") {
		t.Fatal("sonuçta özel anahtar olmamalı")
	}
	if rec.Fingerprint == "" || rec.NotAfter.IsZero() {
		t.Fatalf("parmak izi/notAfter: %+v", rec)
	}
	// DNS-01 köprüsü gerçekten TXT yazdı mı? (mock provider üzerinden
	// yeni bir servis aynı şifreli blob'u çözerek bakabilir.)
	sealed, err := dnsSvc.Store.GetEncryptedCredentials(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := svc.Secrets.Decrypt(sealed)
	if err != nil {
		t.Fatal(err)
	}
	mp, err := dns.BuildMock(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mp.GetRecord("example.internal", "_acme-challenge.flow", "TXT"); !ok {
		t.Fatal("DNS-01 TXT mock sağlayıcıya yazılmalıydı")
	}

	// 2) Renewal failure: ACME rate limit → renewal_failed + class,
	//    CANLI SERTİFİKA MATERYALİ KORUNUR (kriter 4).
	rateFake := fakeca.New()
	rateFake.LimitAfter = 0
	rateFake.RetryAfter = "900"
	rateFake.Start()
	t.Cleanup(rateFake.Close)
	oldACME := svc.ACME
	svc.ACME = &acmex.Manager{DirectoryURL: rateFake.DirURL(), MaxRetry: 1,
		Key: svc.ACME.Key}
	_, renewErr := svc.Renew(ctx, rec.ID)
	if renewErr == nil {
		t.Fatal("rate limit yenileme hatası beklenir")
	}
	if !strings.Contains(renewErr.Error(), "rate_limit") {
		t.Fatalf("sınıflandırma yüzeyde olmalı: %v", renewErr)
	}
	got, _ := svc.Store.Get(ctx, rec.ID)
	if got.State != StateRenewalFail || got.LastErrorClass != "rate_limit" || got.RetryAfterSecs != 900 {
		t.Fatalf("yenileme başarısızlık durumu: %+v", got)
	}
	certEnc, _, err := svc.Store.GetCertMaterial(ctx, rec.ID)
	if err != nil || len(certEnc) == 0 {
		t.Fatal("başarısız yenileme canlı materyali bozmamalı")
	}

	// 3) Başarılı yenileme (gerçek fake'e dön).
	svc.ACME = oldACME
	out2, err := svc.Renew(ctx, rec.ID)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if out2.Record.State != StateIssued {
		t.Fatalf("yenileme sonrası durum: %s", out2.Record.State)
	}

	// 4) Revoke (hesap anahtarıyla; node anahtarı gerekmez).
	revoked, err := svc.Revoke(ctx, rec.ID, 5)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked.State != StateRevoked {
		t.Fatalf("revoked: %s", revoked.State)
	}
	// Idempotent.
	if _, err := svc.Revoke(ctx, rec.ID, 5); err != nil {
		t.Fatalf("ikinci revoke idempotent olmalı: %v", err)
	}
}

func TestFlowIssueFailureClassificationPersisted(t *testing.T) {
	svc, f, dnsSvc := newFlow(t)
	ctx := t.Context()

	if _, err := dnsSvc.CreateProvider(ctx, "p2", "mock", json.RawMessage(`{"api_key":"k2"}`)); err != nil {
		t.Fatal(err)
	}
	providers, _ := dnsSvc.ListProviders(ctx)
	providerID := providers[0].ID

	// Challenge başarısızlığı: sahte CA DNS-01'i geçersiz kılar.
	f.FailDNS01 = true
	rec, err := svc.Issue(ctx, IssueRequest{
		Name: "bad.example.internal", ProviderID: providerID,
		CSRPEM: testCSRPEM(t, []string{"bad.example.internal"}),
	})
	if err == nil {
		t.Fatal("challenge hatası beklenir")
	}
	_ = rec
	// Kayıt pending kalır, hata sınıfı challenge görünür.
	records, _ := svc.Store.List(ctx)
	if len(records) != 1 {
		t.Fatalf("kayıt: %d", len(records))
	}
	r := records[0]
	if r.State != StatePending || r.LastErrorClass != string(acmex.ClassChallenge) {
		t.Fatalf("başarısızlık durumu: %+v", r)
	}
	if !strings.Contains(r.LastError, "example.internal") {
		t.Fatalf("hata mesajı domain içermeli: %s", r.LastError)
	}

	// ACME yapılandırılmamışken dürüst red.
	svc.ACME = nil
	if _, err := svc.Issue(ctx, IssueRequest{Name: "x.example.internal", CSRPEM: testCSRPEM(t, []string{"x.example.internal"})}); !errors_Is(err, ErrACMEUnconfigured) {
		t.Fatalf("ACME yok: %v", err)
	}
}

func errors_Is(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}

func TestParseCSRValidation(t *testing.T) {
	if _, _, err := parseCSR(IssueRequest{Name: "", CSRPEM: "x"}); err == nil {
		t.Fatal("boş ad reddedilmeli")
	}
	if _, _, err := parseCSR(IssueRequest{Name: "a.example.internal", CSRPEM: "PEM değil"}); err == nil {
		t.Fatal("PEM olmayan CSR reddedilmeli")
	}
	// 100 SAN sınırı.
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("d%d.example.internal", i)
	}
	if _, _, err := parseCSR(IssueRequest{Name: "a.example.internal", SANs: many, CSRPEM: testCSRPEM(t, []string{"a.example.internal"})}); err == nil {
		t.Fatal("100'den fazla SAN reddedilmeli")
	}
}
