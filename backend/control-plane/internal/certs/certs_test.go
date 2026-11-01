package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCA(t *testing.T) *CA {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := GenerateCA(certPath, keyPath, "Test CA"); err != nil {
		t.Fatalf("CA üretilemedi: %v", err)
	}
	ca, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("CA yüklenemedi: %v", err)
	}
	return ca
}

func testCSR(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent-csr"},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	return pemBytes, key
}

func TestGenerateAndLoadCA(t *testing.T) {
	ca := testCA(t)
	if !ca.Cert.IsCA {
		t.Fatal("CA sertifikası IsCA olmalı")
	}
	pemOut := ca.CertificatePEM()
	if len(pemOut) < 100 {
		t.Fatal("CA PEM çıktısı bekleniyordu")
	}
	// Anahtar dosyası izni 0600 olmalı.
	keyPath := filepath.Join(t.TempDir(), "x.key")
	_ = keyPath
}

func TestGenerateCAFilePermissions(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")
	if err := GenerateCA(certPath, keyPath, ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("CA anahtar izni 0600 olmalı: %v", info.Mode().Perm())
	}
}

func TestIssueNodeCertBindsImmutableNodeID(t *testing.T) {
	ca := testCA(t)
	csrPEM, _ := testCSR(t)
	const nodeID = "11111111-2222-3333-4444-555555555555"

	leaf, err := ca.IssueNodeCert(csrPEM, nodeID, 24*time.Hour)
	if err != nil {
		t.Fatalf("sertifika üretilemedi: %v", err)
	}
	if leaf.NodeID != nodeID || leaf.Serial == "" || len(leaf.Fingerprint) != 64 {
		t.Fatalf("leaf metadata hatalı: %+v", leaf)
	}

	block, _ := pem.Decode([]byte(leaf.CertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	gotID, err := NodeIDFromSANs(cert.URIs)
	if err != nil || gotID != nodeID {
		t.Fatalf("URI SAN node ID bağlanmalı: %q %v", gotID, err)
	}
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("KeyUsage DigitalSignature olmalı")
	}
	foundClientAuth := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			foundClientAuth = true
		}
	}
	if !foundClientAuth {
		t.Fatal("ExtKeyUsage ClientAuth olmalı")
	}
	if err := cert.VerifyHostname(""); err == nil {
		// VerifyHostname URI SAN ile ilgisiz; hata vermesi normal. Bu satır yalnız
		// panik olmadığını gösterir.
		_ = err
	}
	// CSR imzası CA'ya doğrulanır: sertifika CA zinciriyle doğrulanmalı.
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("sertifika CA zinciriyle doğrulanamadı: %v", err)
	}
}

func TestIssueNodeCertRejectsBadCSRs(t *testing.T) {
	ca := testCA(t)
	csrPEM, _ := testCSR(t)

	// Bozuk PEM.
	if _, err := ca.IssueNodeCert([]byte("not a pem"), "n1", time.Hour); err == nil {
		t.Fatal("bozuk PEM reddedilmeli")
	}
	// Yanlış PEM tipi.
	if _, err := ca.IssueNodeCert([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), "n1", time.Hour); err == nil {
		t.Fatal("yanlış PEM tipi reddedilmeli")
	}
	// İmza bozulmuş CSR.
	tampered := append([]byte(nil), csrPEM...)
	tampered[len(tampered)-10] ^= 0xFF
	if _, err := ca.IssueNodeCert(tampered, "n1", time.Hour); err == nil {
		t.Fatal("imzası bozuk CSR reddedilmeli")
	}
	// CA yok.
	if _, err := (*CA)(nil).IssueNodeCert(csrPEM, "n1", time.Hour); err == nil {
		t.Fatal("CA'sız üretim reddedilmeli")
	}
}

func TestIssueNodeCertTTLBounds(t *testing.T) {
	ca := testCA(t)
	csrPEM, _ := testCSR(t)

	// Geçersiz TTL varsayılana düşer (90 gün), hata değil.
	leaf, err := ca.IssueNodeCert(csrPEM, "n2", -1*time.Hour)
	if err != nil {
		t.Fatalf("geçersiz TTL varsayılana düşmeli: %v", err)
	}
	if d := leaf.NotAfter.Sub(leaf.NotBefore); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("varsayılan TTL 90 gün olmalı: %v", d)
	}
	// Üst sınır 365 gün.
	leaf2, err := ca.IssueNodeCert(csrPEM, "n2", 400*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := leaf2.NotAfter.Sub(leaf2.NotBefore); d > 365*24*time.Hour+time.Hour {
		t.Fatalf("TTL 365 günle sınırlı olmalı: %v", d)
	}
}

func TestNodeIDFromSANs(t *testing.T) {
	// Doğrudan url.URL üretimi ile biçim testi.
	nodeID, err := NodeIDFromSANs(parseURIs(t, "urn:servercontrol:node:abc-123"))
	if err != nil || nodeID != "abc-123" {
		t.Fatalf("node ID çözümlenemedi: %q %v", nodeID, err)
	}
	if _, err := NodeIDFromSANs(parseURIs(t, "https://example.com")); err == nil {
		t.Fatal("ilgili SAN reddedilmeli")
	}
	if _, err := NodeIDFromSANs(nil); err == nil {
		t.Fatal("SAN yokken hata bekleniyordu")
	}
}

func parseURIs(t *testing.T, raw string) []*url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return []*url.URL{u}
}
