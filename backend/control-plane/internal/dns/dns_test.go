package dns

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mockCredsJSON(t *testing.T, key string) []byte {
	t.Helper()
	b, err := json.Marshal(MockCredentials{APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMockProviderCRUD(t *testing.T) {
	p, err := BuildMock(mockCredsJSON(t, "test-key-123"))
	if err != nil {
		t.Fatalf("mock üretimi: %v", err)
	}
	ctx := context.Background()

	// Zones: boş başlar, seed sonrası sıralı döner.
	if zs, _ := p.Zones(ctx); len(zs) != 0 {
		t.Fatalf("başlangıçta bölge olmamalı: %v", zs)
	}
	for _, z := range []string{"example.com", "test.org"} {
		if err := p.SeedZone(z); err != nil {
			t.Fatalf("bölge seed: %v", err)
		}
	}
	zs, err := p.Zones(ctx)
	if err != nil || len(zs) != 2 || zs[0] != "example.com" {
		t.Fatalf("bölgeler: %v err=%v", zs, err)
	}

	// Create.
	rec := Record{Name: "www", Type: "A", Value: "203.0.113.10", TTL: 300}
	got, err := p.ApplyChange(ctx, "example.com", Change{Action: "create", After: &rec})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.TTL != 300 || got.Value != "203.0.113.10" {
		t.Fatalf("create sonucu: %+v", got)
	}
	// Duplicate create → conflict.
	if _, err := p.ApplyChange(ctx, "example.com", Change{Action: "create", After: &rec}); !errors.Is(err, ErrConflict) {
		t.Fatalf("çift create ErrConflict olmalı: %v", err)
	}
	// Update.
	upd := rec
	upd.Value = "203.0.113.11"
	upd.TTL = 600
	if _, err := p.ApplyChange(ctx, "example.com", Change{Action: "update", Before: &rec, After: &upd}); err != nil {
		t.Fatalf("update: %v", err)
	}
	// Read back.
	records, err := p.Records(ctx, "example.com", 100)
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v err=%v", records, err)
	}
	if records[0].Value != "203.0.113.11" || records[0].TTL != 600 {
		t.Fatalf("güncellenen kayıt: %+v", records[0])
	}
	// Delete.
	if _, err := p.ApplyChange(ctx, "example.com", Change{Action: "delete", Before: &upd}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := p.ApplyChange(ctx, "example.com", Change{Action: "delete", Before: &upd}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ikinci delete ErrNotFound olmalı: %v", err)
	}
}

func TestMockCapabilities(t *testing.T) {
	p, err := BuildMock(mockCredsJSON(t, "k"))
	if err != nil {
		t.Fatal(err)
	}
	caps := p.Capabilities()
	if !caps.SupportsDNS01 || !caps.Wildcard {
		t.Fatal("mock DNS-01 + wildcard desteklemeli (kabiliyet bildirimi — kriter 1)")
	}
	if caps.TTLMin != 60 || caps.TTLMax != 86400 {
		t.Fatalf("TTL sınırları: %d..%d", caps.TTLMin, caps.TTLMax)
	}
	if len(caps.RecordTypes) != len(RecordTypes) {
		t.Fatal("kayıt türleri kapalı küme olmalı")
	}
}

func TestBuildMockCredentialContract(t *testing.T) {
	if _, err := BuildMock(mockCredsJSON(t, "")); err == nil {
		t.Fatal("boş api_key reddedilmeli")
	}
	if _, err := BuildMock(mockCredsJSON(t, strings.Repeat("x", 513))); err == nil {
		t.Fatal("aşırı uzun api_key reddedilmeli")
	}
	// Bilinmeyen alan: strict şema reddi.
	if _, err := BuildMock([]byte(`{"api_key":"k","admin":true}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli (strict şema)")
	}
}

func TestValidateRecordAndTTL(t *testing.T) {
	caps := NewMock().Capabilities()

	ok := Record{Name: "@", Type: "A", Value: "203.0.113.5", TTL: 3600}
	if err := ValidateRecord(caps, ok); err != nil {
		t.Fatalf("geçerli kayıt reddedildi: %v", err)
	}
	// TTL alt sınır.
	if err := ValidateRecord(caps, Record{Name: "a", Type: "A", Value: "1.2.3.4", TTL: 59}); !errors.Is(err, ErrTTLBounds) {
		t.Fatalf("TTL 59 reddedilmeli: %v", err)
	}
	// TTL üst sınır.
	if err := ValidateRecord(caps, Record{Name: "a", Type: "A", Value: "1.2.3.4", TTL: 86401}); !errors.Is(err, ErrTTLBounds) {
		t.Fatalf("TTL 86401 reddedilmeli: %v", err)
	}
	// Bilinmeyen tür.
	if err := ValidateRecord(caps, Record{Name: "a", Type: "SRV", Value: "1 2 3", TTL: 300}); !errors.Is(err, ErrTypeUnsupport) {
		t.Fatalf("SRV reddedilmeli: %v", err)
	}
	// IPv4 sekizlisi.
	if err := ValidateRecord(caps, Record{Name: "a", Type: "A", Value: "256.1.1.1", TTL: 300}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("256.1.1.1 reddedilmeli: %v", err)
	}
	// IPv6.
	if err := ValidateRecord(caps, Record{Name: "a6", Type: "AAAA", Value: "2001:db8::1", TTL: 300}); err != nil {
		t.Fatalf("geçerli AAAA: %v", err)
	}
	if err := ValidateRecord(caps, Record{Name: "a6", Type: "AAAA", Value: "not-ip", TTL: 300}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("geçersiz AAAA reddedilmeli: %v", err)
	}
	// CNAME hedefi.
	if err := ValidateRecord(caps, Record{Name: "c", Type: "CNAME", Value: "target.example.com", TTL: 300}); err != nil {
		t.Fatalf("geçerli CNAME: %v", err)
	}
	if err := ValidateRecord(caps, Record{Name: "c", Type: "CNAME", Value: "-bad-.example.com", TTL: 300}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("geçersiz CNAME reddedilmeli: %v", err)
	}
	// MX.
	if err := ValidateRecord(caps, Record{Name: "@", Type: "MX", Value: "10 mail.example.com", TTL: 300}); err != nil {
		t.Fatalf("geçerli MX: %v", err)
	}
	if err := ValidateRecord(caps, Record{Name: "@", Type: "MX", Value: "70000 mail.example.com", TTL: 300}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("MX önceliği reddedilmeli: %v", err)
	}
}

func TestValidRecordName(t *testing.T) {
	if !ValidRecordName("www", true) || !ValidRecordName("@", true) {
		t.Fatal("www ve @ geçerli olmalı")
	}
	if ValidRecordName("*", true) {
		t.Fatal("yalnız '*' geçersiz (*.sub biçimi olmalı)")
	}
	if !ValidRecordName("*.example.com", true) {
		t.Fatal("wildcard destekliyorsa *.sub geçerli olmalı")
	}
	if ValidRecordName("*.example.com", false) {
		t.Fatal("wildcard desteklemeyen sağlayıcıda *.sub reddedilmeli")
	}
	if ValidRecordName("..bad", true) || ValidRecordName("Bad!", true) || ValidRecordName("", true) {
		t.Fatal("geçersiz adlar reddedilmeli")
	}
}

func TestValidZoneName(t *testing.T) {
	if !ValidZoneName("example.com") || !ValidZoneName("a-b.co") {
		t.Fatal("geçerli bölgeler kabul edilmeli")
	}
	if ValidZoneName("example.com.") || ValidZoneName("-bad.com") || ValidZoneName("") || ValidZoneName("örnek.com") {
		t.Fatal("geçersiz bölgeler reddedilmeli (sonda nokta, tire başlangıç, boş, unicode)")
	}
}

func TestDiff(t *testing.T) {
	existing := []Record{{Name: "www", Type: "A", Value: "1.2.3.4", TTL: 300}}
	// Yeni kayıt → create.
	ch, err := Diff(existing, Record{Name: "mail", Type: "A", Value: "1.2.3.5", TTL: 300})
	if err != nil || ch.Action != "create" {
		t.Fatalf("create diff: %+v err=%v", ch, err)
	}
	// Var olan → update + before doldurulur.
	ch, err = Diff(existing, Record{Name: "www", Type: "A", Value: "9.9.9.9", TTL: 600})
	if err != nil || ch.Action != "update" {
		t.Fatalf("update diff: %+v err=%v", ch, err)
	}
	if ch.Before == nil || ch.Before.Value != "1.2.3.4" || ch.After.Value != "9.9.9.9" {
		t.Fatalf("diff before/after: %+v", ch)
	}
	// Aynı değer+TTL → yine update (no-op) ama before=after.
	ch, err = Diff(existing, existing[0])
	if err != nil || ch.Action != "update" || ch.Before.Value != ch.After.Value {
		t.Fatalf("no-op diff: %+v err=%v", ch, err)
	}
}

func TestMockGetRecordForDNS01(t *testing.T) {
	// Sahte ACME sunucusu DNS-01 doğrulamasını bu okuma üzerinden yapar.
	p := NewMock()
	_ = p.SeedZone("example.com")
	txt := Record{Name: "_acme-challenge", Type: "TXT", Value: "token-degeri", TTL: 60}
	if _, err := p.ApplyChange(context.Background(), "example.com", Change{Action: "create", After: &txt}); err != nil {
		t.Fatal(err)
	}
	got, ok := p.GetRecord("example.com", "_acme-challenge", "TXT")
	if !ok || got.Value != "token-degeri" {
		t.Fatalf("TXT okuma: %+v ok=%v", got, ok)
	}
}
