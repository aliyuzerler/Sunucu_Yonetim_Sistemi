package acmex

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/acmex/fakeca"
	"golang.org/x/crypto/acme"
)

// newTestManager: test için ECDSA hesap anahtarlı, hızlı hata veren Manager.
func newTestManager(t *testing.T, dirURL string) *Manager {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &Manager{
		Key:          key,
		DirectoryURL: dirURL,
		Contact:      []string{"mailto:ops@example.internal"},
		MaxRetry:     1, // hızlı sınıflandırma; yeniden deneme yok
	}
}

// setterFor: gerçek Faz 09 DNS-01 köprüsünün davranışı — TXT upsert
// (create; varsa update). testTXT kayıt defteri fake doğrulaması içindir.
func setterFor(t *testing.T, store map[string]string) DNS01Setter {
	t.Helper()
	return func(_ context.Context, domain, value string) error {
		if len(value) > 512 {
			return errors.New("TXT değeri aşırı uzun")
		}
		store["_acme-challenge."+domain] = value
		return nil
	}
}

func keyAuthzDigest(account *ecdsa.PublicKey, token string) string {
	tp, err := acme.JWKThumbprint(account)
	if err != nil {
		return ""
	}
	return fakeca.DNS01Digest(token, tp)
}

func makeCSR(t *testing.T, domains []string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: domains[0]},
		DNSNames:           domains,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestClassifyRateLimit(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("Retry-After", "120")
	err := &acme.Error{StatusCode: http.StatusTooManyRequests, ProblemType: "urn:ietf:params:acme:error:rateLimited", Header: hdr}
	cls, d := Classify(err)
	if cls != ClassRateLimit || d != 120*time.Second {
		t.Fatalf("rate limit sınıflandırması: cls=%s retry=%v", cls, d)
	}
	// acme.RateLimit yardımcısı da aynı hatayı rate limit görmeli.
	if _, ok := acme.RateLimit(err); !ok {
		t.Fatal("acme.RateLimit bu hatayı rate limit olarak görmeli")
	}
}

func TestClassifyChallengeAndOthers(t *testing.T) {
	if cls, _ := Classify(&acme.AuthorizationError{Identifier: "example.com"}); cls != ClassChallenge {
		t.Fatalf("challenge sınıfı: %s", cls)
	}
	if cls, _ := Classify(&acme.OrderError{Status: "invalid"}); cls != ClassOrderInval {
		t.Fatalf("order sınıfı: %s", cls)
	}
	if cls, _ := Classify(&url.Error{Op: "Post", Err: errors.New("conn refused")}); cls != ClassNetwork {
		t.Fatalf("network sınıfı: %s", cls)
	}
	if cls, _ := Classify(fmt.Errorf("başka")); cls != ClassACMEOther {
		t.Fatalf("other sınıfı: %s", cls)
	}
	if cls, _ := Classify(nil); cls != ClassNone {
		t.Fatalf("nil sınıfı boş olmalı: %s", cls)
	}
	if cls, _ := Classify(fmt.Errorf("%w: eksik SAN", ErrCSRInvalid)); cls != ClassCSR {
		t.Fatalf("csr sınıfı: %s", cls)
	}
}

func TestIssueHappyPath(t *testing.T) {
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	mgr := newTestManager(t, f.DirURL())
	ctx := context.Background()
	domains := []string{"example.internal", "www.example.internal"}
	csr := makeCSR(t, domains)

	txts := map[string]string{}
	// Fake, TXT'nin gerçek RFC 8555 digest'i olduğunu doğrular.
	f.VerifyDNS01 = func(domain, token string) error {
		want := keyAuthzDigest(&mgr.Key.PublicKey, token)
		got, ok := txts["_acme-challenge."+domain]
		if !ok {
			return fmt.Errorf("TXT yayımlanmamış: %s", domain)
		}
		if got != want {
			return fmt.Errorf("TXT değeri uyuşmuyor (%s)", domain)
		}
		return nil
	}

	res, err := mgr.Issue(ctx, domains, csr, setterFor(t, txts))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	leaf, err := x509.ParseCertificate(res.CertDER)
	if err != nil {
		t.Fatalf("yaprak ayrıştırma: %v", err)
	}
	if len(leaf.DNSNames) != len(domains) {
		t.Fatalf("SAN sayısı: %v", leaf.DNSNames)
	}
	for i, d := range domains {
		if leaf.DNSNames[i] != d {
			t.Fatalf("SAN: %v", leaf.DNSNames)
		}
	}
	if res.NotAfter.Before(time.Now()) {
		t.Fatal("NotAfter gelecekte olmalı")
	}
	if res.Serial == "" {
		t.Fatal("serial boş olmamalı")
	}
	if len(res.ChainDER) != 1 {
		t.Fatalf("zincir: %d ara sertifika", len(res.ChainDER))
	}
	// Fake sunucu gerçekten sertifika üretmiş ve yayımlamış olmalı.
	if leaf.NotAfter.Sub(leaf.NotBefore) < 80*24*time.Hour {
		t.Fatalf("yaprak ömür beklenmedik: %v", leaf.NotAfter.Sub(leaf.NotBefore))
	}
}

func TestIssueRateLimitSurfaced(t *testing.T) {
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	f.LimitAfter = 0 // İLK new-order'da 429
	f.RetryAfter = "120"

	mgr := newTestManager(t, f.DirURL())
	_, err := mgr.Issue(context.Background(), []string{"example.internal"}, makeCSR(t, []string{"example.internal"}), setterFor(t, map[string]string{}))
	if err == nil {
		t.Fatal("rate limit altında hata beklenir")
	}
	cls, retry := Classify(err)
	if cls != ClassRateLimit {
		t.Fatalf("sınıf rate_limit olmalı: %s (%v)", cls, err)
	}
	if retry != 120*time.Second {
		t.Fatalf("Retry-After 120s yüzeye çıkmalı: %v", retry)
	}
	if !strings.Contains(err.Error(), "rate limit") && !strings.Contains(strings.ToLower(err.Error()), "ratelimit") {
		// Hata metni sınıflandırmayı taşımak zorunda değil ama en azından
		// ACME detayını içermeli.
		t.Logf("hata metni: %v", err)
	}
}

func TestIssueChallengeFailureSurfaced(t *testing.T) {
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	f.FailDNS01 = true

	mgr := newTestManager(t, f.DirURL())
	_, err := mgr.Issue(context.Background(), []string{"example.internal"}, makeCSR(t, []string{"example.internal"}), setterFor(t, map[string]string{}))
	if err == nil {
		t.Fatal("challenge başarısızlığında hata beklenir")
	}
	cls, _ := Classify(err)
	if cls != ClassChallenge {
		t.Fatalf("sınıf challenge olmalı: %s (%v)", cls, err)
	}
}

func TestIssueSetterErrorIsNotChallenge(t *testing.T) {
	// DNS-01 köprüsü (sağlayıcı) hata verirse: challenge sınıfı DEĞİL,
	// dürüst köprü hatası — sağlayıcı sorunu ile CA reddi karışmamalı.
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	mgr := newTestManager(t, f.DirURL())
	setter := func(context.Context, string, string) error { return errors.New("sağlayıcı kapalı") }
	_, err := mgr.Issue(context.Background(), []string{"example.internal"}, makeCSR(t, []string{"example.internal"}), setter)
	if err == nil {
		t.Fatal("setter hatası yüzeye çıkmalı")
	}
	if strings.Contains(err.Error(), "yetkilendirme doğrulanamadı") {
		t.Fatal("setter hatası challenge doğrulama hatası gibi görünmemeli")
	}
	cls, _ := Classify(err)
	if cls == ClassChallenge {
		t.Fatalf("sağlayıcı hatası challenge olarak sınıflanmamalı: %v", err)
	}
}

func TestIssueCSRMismatchRejected(t *testing.T) {
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	mgr := newTestManager(t, f.DirURL())
	// CSR tek domain, istek iki domain → ağ trafiği ÖNCE ausız red.
	_, err := mgr.Issue(context.Background(), []string{"a.example.internal", "b.example.internal"}, makeCSR(t, []string{"a.example.internal"}), setterFor(t, map[string]string{}))
	if !errors.Is(err, ErrCSRInvalid) {
		t.Fatalf("CSR uyuşmazlığı ErrCSRInvalid olmalı: %v", err)
	}
	cls, _ := Classify(err)
	if cls != ClassCSR {
		t.Fatalf("csr sınıfı: %s", cls)
	}
}

func TestRevokeByAccount(t *testing.T) {
	f := fakeca.New().Start()
	t.Cleanup(f.Close)
	mgr := newTestManager(t, f.DirURL())
	ctx := context.Background()

	txts := map[string]string{}
	f.VerifyDNS01 = func(domain, token string) error {
		want := keyAuthzDigest(&mgr.Key.PublicKey, token)
		if txts["_acme-challenge."+domain] != want {
			return fmt.Errorf("TXT uyuşmuyor (%s)", domain)
		}
		return nil
	}
	res, err := mgr.Issue(ctx, []string{"example.internal"}, makeCSR(t, []string{"example.internal"}), setterFor(t, txts))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := mgr.Revoke(ctx, res.CertDER, 5); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if f.RevokeCount != 1 {
		t.Fatalf("revoke çağrısı: %d", f.RevokeCount)
	}
	parsed, err := x509.ParseCertificate(f.RevokedCert)
	if err != nil {
		t.Fatalf("fake'a ulaşan veri DER sertifika değil: %v", err)
	}
	issued, err := x509.ParseCertificate(res.CertDER)
	if err != nil {
		t.Fatalf("üretilen yaprak ayrıştırma: %v", err)
	}
	if parsed.SerialNumber.Cmp(issued.SerialNumber) != 0 {
		t.Fatal("revoke edilen sertifika, üretilenle aynı serial olmalı")
	}
	if !strings.Contains(f.LastRevoke, "reason=5") {
		t.Fatalf("gerekçe iletildi mi: %s", f.LastRevoke)
	}
	// Geçersiz gerekçe reddi ağdan önce.
	if err := mgr.Revoke(ctx, res.CertDER, 99); err == nil {
		t.Fatal("reason 99 reddedilmeli")
	}
}

func TestLoadOrCreateAccountKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme", "account.key")
	k1, err := LoadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("üretim: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("anahtar dosya izni 0600 olmalı: %v", st.Mode().Perm())
	}
	k2, err := LoadOrCreateAccountKey(path)
	if err != nil {
		t.Fatalf("yeniden yükleme: %v", err)
	}
	if k1.D.String() != k2.D.String() {
		t.Fatal("aynı dosyadan aynı anahtar yüklenmeli")
	}
	bad := filepath.Join(dir, "bozuk.key")
	_ = os.WriteFile(bad, []byte("bu pem değil"), 0o600)
	if _, err := LoadOrCreateAccountKey(bad); err == nil {
		t.Fatal("PEM olmayan dosya reddedilmeli")
	}
}

func TestValidateCSRUpfront(t *testing.T) {
	good := makeCSR(t, []string{"ok.example.internal"})
	if err := validateCSR([]string{"ok.example.internal"}, good); err != nil {
		t.Fatalf("geçerli CSR: %v", err)
	}
	if err := validateCSR([]string{"other.example.internal"}, good); !errors.Is(err, ErrCSRInvalid) {
		t.Fatalf("SAN uyuşmazlığı: %v", err)
	}
	if err := validateCSR([]string{"x.example.internal"}, []byte("DER değil")); !errors.Is(err, ErrCSRInvalid) {
		t.Fatalf("bozuk DER: %v", err)
	}
	// PEM sarmalayıcı: acme istemcisi DER bekler; PEM gövdesi reddedilir.
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: good})
	if err := validateCSR([]string{"ok.example.internal"}, pemBytes); !errors.Is(err, ErrCSRInvalid) {
		t.Fatalf("PEM girdisi reddedilmeli: %v", err)
	}
}
