//go:build unix

package tlsops

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA issues real self-signed certificates for the hermetic runner tests.
type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{key: key, cert: cert}
}

// issue creates a leaf for domains signed by the test CA and returns the
// PEM chain (leaf + CA) plus the leaf's matching private key.
func (c *testCA) issue(t *testing.T, domains []string, notAfter time.Time) (certPEM, chainPEM string, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domains[0]},
		DNSNames:     domains,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw}))
	return leafPEM, caPEM, key
}

func pubFingerprint(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return FingerprintOf(der)
}

// runnerFor builds a hermetic Runner: nginx -t and systemctl are recorded
// instead of executed.
type fakeExec struct {
	calls    []string
	nginxOK  bool
	reloadOK bool
}

func (f *fakeExec) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch name {
	case "nginx":
		if f.nginxOK {
			return []byte("syntax is ok"), nil
		}
		return []byte("nginx: configuration file test failed"), errors.New("exit 1")
	case "systemctl":
		if f.reloadOK {
			return nil, nil
		}
		return []byte("reload failed"), errors.New("exit 1")
	}
	return nil, nil
}

func newTestRunner(t *testing.T, fe *fakeExec) *Runner {
	t.Helper()
	r := NewRunner()
	r.CertDir = t.TempDir()
	r.execFn = fe.run
	r.lookPath = func(string) (string, error) { return "/usr/sbin/nginx", nil }
	return r
}

func TestRequestGeneratesAndReusesKey(t *testing.T) {
	r := newTestRunner(t, &fakeExec{nginxOK: true, reloadOK: true})

	res, err := r.Request(context.Background(), "example.internal", []string{"www.example.internal"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if res.KeyReused {
		t.Fatal("ilk çağrı yeni anahtar üretmeli")
	}
	if !strings.Contains(res.CSRPEM, "BEGIN CERTIFICATE REQUEST") {
		t.Fatalf("CSR PEM: %s", res.CSRPEM[:40])
	}
	if len(res.SANs) != 2 || res.SANs[0] != "example.internal" {
		t.Fatalf("SANs: %v", res.SANs)
	}
	// CSR, özel anahtarı İÇERMEZ (kriter: anahtar node'dan çıkmaz).
	if strings.Contains(res.CSRPEM, "PRIVATE KEY") {
		t.Fatal("CSR yanıtı özel anahtar içermemeli")
	}

	// Anahtar dosyası 0600 olmalı.
	info, err := os.Stat(filepath.Join(r.CertDir, "example.internal", KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("anahtar izni 0600 olmalı: %v", info.Mode().Perm())
	}

	// İkinci çağrı aynı anahtarı YENİDEN KULLANIR (aynı parmak izi).
	res2, err := r.Request(context.Background(), "example.internal", nil)
	if err != nil {
		t.Fatalf("request-2: %v", err)
	}
	if !res2.KeyReused || res2.KeyFingerprint != res.KeyFingerprint {
		t.Fatalf("anahtar yeniden kullanılmalı: %+v vs %+v", res2, res)
	}
}

func TestRequestRejectsInvalidDomains(t *testing.T) {
	r := newTestRunner(t, &fakeExec{})
	if _, err := r.Request(context.Background(), "Bad!.internal", nil); !errors.Is(err, ErrInvalidDomain) {
		t.Fatalf("geçersiz domain: %v", err)
	}
	if _, err := r.Request(context.Background(), "example.internal", []string{"ok.example.internal", "ok.example.internal", "ok2.example.internal"}); err != nil {
		t.Fatalf("tekrarlı SAN normalize edilmeli: %v", err)
	}
	if _, err := r.Request(context.Background(), "", nil); !errors.Is(err, ErrInvalidDomain) {
		t.Fatal("boş domain reddedilmeli")
	}
}

func TestInstallHappyPath(t *testing.T) {
	ca := newTestCA(t)
	fe := &fakeExec{nginxOK: true, reloadOK: true}
	r := newTestRunner(t, fe)

	// Anahtarı üret.
	if _, err := r.Request(context.Background(), "example.internal", nil); err != nil {
		t.Fatal(err)
	}
	// Bu node'un anahtarıyla eşleşen sertifikayı üret.
	keyPath := filepath.Join(r.CertDir, "example.internal", KeyFile)
	keyRaw, _ := os.ReadFile(keyPath)
	nodeKey, err := loadECKey(keyRaw)
	if err != nil {
		t.Fatal(err)
	}
	// node anahtarıyla imzalı sertifika üret (leaf public = node key public).
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "example.internal"},
		DNSNames:     []string{"example.internal"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}, ca.cert, &nodeKey.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))

	res, err := r.Install(context.Background(), "example.internal", leafPEM, "")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Reloaded || res.RollbackUsed {
		t.Fatalf("ilk kurulum: %+v", res)
	}
	// nginx -t ve reload çağrıldı mı?
	joined := strings.Join(fe.calls, " | ")
	if !strings.Contains(joined, "nginx -t") || !strings.Contains(joined, "systemctl reload nginx") {
		t.Fatalf("kapı çağrıları: %v", fe.calls)
	}
	// Diskte: fullchain var, rollback yok (önceki sertifika yoktu).
	if _, err := os.Stat(filepath.Join(r.CertDir, "example.internal", CertFile)); err != nil {
		t.Fatal("fullchain.pem yazılmalıydı")
	}
	if _, err := os.Stat(filepath.Join(r.CertDir, "example.internal", CertFile+RollbackSuffix)); !os.IsNotExist(err) {
		t.Fatal("önceki sertifika yokken rollback olmamalı")
	}
	// Status raporlar.
	st, err := r.Status(context.Background())
	if err != nil || len(st) != 1 {
		t.Fatalf("status: %v %v", st, err)
	}
	if st[0].Domain != "example.internal" || !st[0].KeyPresent || st[0].DaysLeft <= 80 {
		t.Fatalf("status satırı: %+v", st[0])
	}
}

func TestInstallFailedSyntaxGateRestoresPreviousCert(t *testing.T) {
	ca := newTestCA(t)
	r := newTestRunner(t, &fakeExec{nginxOK: true, reloadOK: true})
	ctx := context.Background()

	if _, err := r.Request(ctx, "example.internal", nil); err != nil {
		t.Fatal(err)
	}
	keyRaw, _ := os.ReadFile(filepath.Join(r.CertDir, "example.internal", KeyFile))
	nodeKey, _ := loadECKey(keyRaw)
	mkLeaf := func(serial int64, days int) string {
		der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: "example.internal"},
			DNSNames:     []string{"example.internal"},
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		}, ca.cert, &nodeKey.PublicKey, ca.key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	// 1) İyi sertifika kurulur (nginx -t OK).
	if _, err := r.Install(ctx, "example.internal", mkLeaf(1, 90), ""); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(filepath.Join(r.CertDir, "example.internal", CertFile))

	// 2) nginx -t ARTIK BAŞARISIZ → yeni sertifika kurulamaz.
	r2 := newTestRunner(t, &fakeExec{nginxOK: false})
	r2.CertDir = r.CertDir
	_, err := r2.Install(ctx, "example.internal", mkLeaf(2, 60), "")
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("syntax hatası beklenir: %v", err)
	}
	// Canlı sertifika korunur (rollback geri yükledi).
	current, _ := os.ReadFile(filepath.Join(r.CertDir, "example.internal", CertFile))
	if string(current) != string(good) {
		t.Fatal("başarısız kurulum canlı sertifikayı BOZMAMALI (kriter 4)")
	}
}

func TestInstallKeyPairMismatch(t *testing.T) {
	ca := newTestCA(t)
	r := newTestRunner(t, &fakeExec{nginxOK: true})
	ctx := context.Background()

	if _, err := r.Request(ctx, "example.internal", nil); err != nil {
		t.Fatal(err)
	}
	// BAŞKA bir anahtarla üretilmiş sertifika.
	leafPEM, _, otherKey := ca.issue(t, []string{"example.internal"}, time.Now().Add(30*24*time.Hour))
	_ = otherKey
	_, err := r.Install(ctx, "example.internal", leafPEM, "")
	if !errors.Is(err, ErrKeyPairMismatch) {
		t.Fatalf("çift eşleşmezliği tipli olmalı: %v", err)
	}
	// Hiçbir dosya değişmedi: fullchain hâlâ yok.
	if _, err := os.Stat(filepath.Join(r.CertDir, "example.internal", CertFile)); !os.IsNotExist(err) {
		t.Fatal("eşleşmezlikte dosya yazılmamalı")
	}
}

func TestInstallWithoutKeyRefused(t *testing.T) {
	ca := newTestCA(t)
	r := newTestRunner(t, &fakeExec{nginxOK: true})
	leaf, chain, _ := ca.issue(t, []string{"example.internal"}, time.Now().Add(24*time.Hour))
	_, err := r.Install(context.Background(), "example.internal", leaf, chain)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("anahtarsız kurulum ErrNotFound olmalı: %v", err)
	}
}

func TestInstallExpiredCertRefused(t *testing.T) {
	ca := newTestCA(t)
	r := newTestRunner(t, &fakeExec{nginxOK: true})
	ctx := context.Background()
	if _, err := r.Request(ctx, "example.internal", nil); err != nil {
		t.Fatal(err)
	}
	keyRaw, _ := os.ReadFile(filepath.Join(r.CertDir, "example.internal", KeyFile))
	nodeKey, _ := loadECKey(keyRaw)
	der, _ := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(9),
		Subject:      pkix.Name{CommonName: "example.internal"},
		DNSNames:     []string{"example.internal"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour),
	}, ca.cert, &nodeKey.PublicKey, ca.key)
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if _, err := r.Install(ctx, "example.internal", leafPEM, ""); !errors.Is(err, ErrCertExpired) {
		t.Fatalf("süresi dolmuş sertifika reddedilmeli: %v", err)
	}
}

func TestInstallSANCoverageRequired(t *testing.T) {
	ca := newTestCA(t)
	r := newTestRunner(t, &fakeExec{nginxOK: true})
	ctx := context.Background()
	if _, err := r.Request(ctx, "example.internal", nil); err != nil {
		t.Fatal(err)
	}
	keyRaw, _ := os.ReadFile(filepath.Join(r.CertDir, "example.internal", KeyFile))
	nodeKey, _ := loadECKey(keyRaw)
	der, _ := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(10),
		Subject:      pkix.Name{CommonName: "other.example.internal"},
		DNSNames:     []string{"other.example.internal"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}, ca.cert, &nodeKey.PublicKey, ca.key)
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if _, err := r.Install(ctx, "example.internal", leafPEM, ""); !errors.Is(err, ErrCertInvalid) {
		t.Fatalf("SAN kapsamayan sertifika reddedilmeli: %v", err)
	}
}

func TestStatusEmptyAndForeignDirs(t *testing.T) {
	r := newTestRunner(t, &fakeExec{})
	// Boş dizin: boş liste.
	st, err := r.Status(context.Background())
	if err != nil || len(st) != 0 {
		t.Fatalf("boş durum: %v %v", st, err)
	}
	// Yabancı dizin adı raporlanmaz.
	_ = os.MkdirAll(filepath.Join(r.CertDir, "Not-A-Domain"), 0o750)
	st, _ = r.Status(context.Background())
	if len(st) != 0 {
		t.Fatalf("yabancı dizin raporlanmamalı: %+v", st)
	}
}

func TestNormalizeDomains(t *testing.T) {
	got, err := NormalizeDomains("example.internal", []string{"www.example.internal", "example.internal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "example.internal" {
		t.Fatalf("tekrar düşürülmeli: %v", got)
	}
	// Büyük harf SAN geçersiz (lowercase allowlist).
	if _, err := NormalizeDomains("example.internal", []string{"WWW.EXAMPLE.INTERNAL"}); !errors.Is(err, ErrInvalidDomain) {
		t.Fatalf("büyük harf SAN reddedilmeli: %v", err)
	}
}
