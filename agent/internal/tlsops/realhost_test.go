//go:build linux

// Faz 09 gerçek-host kanıt testi: runner'da GERÇEK nginx ile sertifika
// yaşam döngüsü. EXPECTED_TLS=1 gate'i CI-17 dersidir: env set edildiğinde
// gerçek kanıt ZORUNLUDUR — sessiz atlanamaz. CI agent-web job'ında koşar.
package tlsops

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfSignedFor builds a real self-signed certificate whose public key IS
// the node's private key (the only way a pair check can pass).
func selfSignedFor(t *testing.T, nodeKeyPEM []byte, domain string, days int) string {
	t.Helper()
	block, _ := pem.Decode(nodeKeyPEM)
	if block == nil {
		t.Fatal("anahtar PEM bozuk")
	}
	nodeKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &nodeKey.PublicKey, nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestRealTLSLifecycle(t *testing.T) {
	if os.Getenv("EXPECTED_TLS") != "1" {
		t.Skip("EXPECTED_TLS=1 değil; gerçek-host kanıtı yalnız CI'da koşar")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Fatalf("EXPECTED_TLS=1 ama nginx yok — sessiz atlama YASAK (CI-17): %v", err)
	}

	r := NewRunner()
	// Test alanı: gerçek /etc/nginx/ssl/srvctl'i BOZMAZ; ayrı alt dizin.
	// nginx -t yine SİSTEM config'ini doğrular (gerçek kanıt).
	r.CertDir = "/etc/nginx/ssl/srvctl-citest"
	t.Cleanup(func() { _ = os.RemoveAll(r.CertDir) })
	_ = os.RemoveAll(r.CertDir)

	ctx := context.Background()

	// 1) web.cert.request: anahtar node'da doğar, CSR döner.
	req, err := r.Request(ctx, "citest.example.internal", []string{"www.citest.example.internal"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if req.KeyReused || !strings.Contains(req.CSRPEM, "BEGIN CERTIFICATE REQUEST") {
		t.Fatalf("request sonucu: %+v", req)
	}
	// CSR özel anahtar İÇERMEZ (kriter: anahtar node'dan çıkmaz).
	if strings.Contains(req.CSRPEM, "PRIVATE KEY") {
		t.Fatal("CSR özel anahtar içermemeli")
	}
	info, err := os.Stat(filepath.Join(r.CertDir, "citest.example.internal", KeyFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("anahtar izni: %v %v", info, err)
	}

	// 2) Node anahtarıyla eşleşen gerçek self-signed sertifika üret.
	keyRaw, _ := os.ReadFile(filepath.Join(r.CertDir, "citest.example.internal", KeyFile))
	certPEM := selfSignedFor(t, keyRaw, "citest.example.internal", 90)

	// 3) web.cert.install: gerçek nginx -t kapısı + gerçek reload.
	ins, err := r.Install(ctx, "citest.example.internal", certPEM, "")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !ins.Reloaded {
		t.Fatalf("reload sinyali beklenir: %+v", ins)
	}
	fullchain := filepath.Join(r.CertDir, "citest.example.internal", CertFile)
	if _, err := os.Stat(fullchain); err != nil {
		t.Fatalf("fullchain yazılmalıydı: %v", err)
	}

	// 4) web.cert.status: kurulu sertifika raporlanır.
	st, err := r.Status(ctx)
	if err != nil || len(st) != 1 {
		t.Fatalf("status: %+v err=%v", st, err)
	}
	if st[0].Domain != "citest.example.internal" || !st[0].KeyPresent || st[0].DaysLeft <= 80 {
		t.Fatalf("status satırı: %+v", st[0])
	}

	// 5) NEGATİF: eşleşmeyen anahtarla üretilmiş sertifika reddedilir ve
	//    CANLI SERTİFİKA korunur (kriter 4 gerçek-host kanıtı).
	_, otherPEM, _ := newTestCA(t).issue(t, []string{"citest.example.internal"}, time.Now().Add(30*24*time.Hour))
	_, err = r.Install(ctx, "citest.example.internal", otherPEM, "")
	if !errors.Is(err, ErrKeyPairMismatch) {
		t.Fatalf("çift eşleşmezliği tipli olmalı: %v", err)
	}
	current, _ := os.ReadFile(fullchain)
	if !strings.Contains(string(current), "BEGIN CERTIFICATE") {
		t.Fatal("canlı fullchain korunmalıydı")
	}
	if !certsEqual(current, []byte(certPEM)) {
		t.Fatal("başarısız install canlı sertifikayı değiştirmemeli")
	}

	// 6) nginx config hâlâ geçerli (sistem bütünlüğü kanıtı).
	out, err := exec.Command("nginx", "-t").CombinedOutput()
	if err != nil {
		t.Fatalf("sonrası nginx -t: %v: %s", err, out)
	}
}

func certsEqual(a, b []byte) bool {
	pa, _ := pem.Decode(a)
	pb, _ := pem.Decode(b)
	return pa != nil && pb != nil && string(pa.Bytes) == string(pb.Bytes)
}
