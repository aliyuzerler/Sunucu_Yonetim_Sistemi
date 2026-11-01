package certmgmt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestEffectiveStateOf(t *testing.T) {
	now := time.Now()
	rec := Record{State: StateIssued, NotAfter: now.Add(24 * time.Hour)}
	if got := EffectiveStateOf(rec, now); got != StateIssued {
		t.Fatalf("gelecek NotAfter: %s", got)
	}
	expired := Record{State: StateIssued, NotAfter: now.Add(-time.Hour)}
	if got := EffectiveStateOf(expired, now); got != StateExpired {
		t.Fatalf("geçmiş NotAfter effective=expired olmalı: %s", got)
	}
	// Revoked kayıtlar expired'a çekilmez (denetim bütünlüğü).
	revoked := Record{State: StateRevoked, NotAfter: now.Add(-time.Hour)}
	if got := EffectiveStateOf(revoked, now); got != StateRevoked {
		t.Fatalf("revoked kalır: %s", got)
	}
	pending := Record{State: StatePending, NotAfter: now.Add(-time.Hour)}
	if got := EffectiveStateOf(pending, now); got != StatePending {
		t.Fatalf("pending NotAfter'sız korunur: %s", got)
	}
}

func TestParseLeafPEMAndFingerprint(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "example.internal"},
		DNSNames:     []string{"example.internal"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)

	leaf, err := ParseLeafPEM(bundle)
	if err != nil {
		t.Fatalf("yaprak ayrıştırma: %v", err)
	}
	if leaf.SerialNumber.Int64() != 42 {
		t.Fatalf("serial: %v", leaf.SerialNumber)
	}
	fp := FingerprintOf(der)
	if len(fp) != 64 {
		t.Fatalf("parmak izi uzunluğu: %d", len(fp))
	}
	// Deterministik.
	if fp != FingerprintOf(der) {
		t.Fatal("parmak izi deterministik olmalı")
	}
	if _, err := ParseLeafPEM([]byte("PEM değil")); err == nil {
		t.Fatal("PEM olmayan girdi reddedilmeli")
	}
}

func TestDaysLeft(t *testing.T) {
	now := time.Now()
	if d := DaysLeft(now.Add(45*24*time.Hour), now); d != 45 {
		t.Fatalf("45 gün: %d", d)
	}
	if d := DaysLeft(now.Add(-time.Hour), now); d != 0 {
		t.Fatalf("geçmiş 0 olmalı: %d", d)
	}
	if d := DaysLeft(time.Time{}, now); d != 0 {
		t.Fatalf("sıfır zaman 0: %d", d)
	}
}
