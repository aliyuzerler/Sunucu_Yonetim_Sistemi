package tasks

import (
	"encoding/json"
	"testing"
)

func faz09Validators() map[string]func(json.RawMessage) error {
	return map[string]func(json.RawMessage) error{
		"dns.zone.list":     ValidateDNSProviderID,
		"dns.record.list":   ValidateDNSZone,
		"dns.record.diff":   ValidateDNSRecord,
		"dns.record.apply":  ValidateDNSRecord,
		"dns.record.delete": ValidateDNSRecordDelete,
		"cert.issue":        ValidateCertIssue,
		"cert.renew":        ValidateCertID,
		"cert.revoke":       ValidateCertID,
		"web.cert.request":  ValidateWebCertRequest,
		"web.cert.install":  ValidateWebCertInstall,
		"web.cert.status":   ValidateWebCertStatus,
	}
}

func TestFaz09RegistryRegistered(t *testing.T) {
	reg, err := BuildDefaultRegistry(&fakeTagSetter{}, Faz09Services{}, Faz14Services{}, Faz15Services{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for name, validate := range faz09Validators() {
		spec, ok := reg.Spec(name)
		if !ok {
			t.Fatalf("%s kayıtlı olmalı", name)
		}
		if spec.Validate == nil {
			t.Fatalf("%s validator'ı olmalı", name)
		}
		_ = validate
		// RBAC eşlemesi (kriter bağlamı: her işlem için RBAC).
		switch name {
		case "dns.zone.list", "dns.record.list", "dns.record.diff":
			if spec.RequiredPerm != "dns:read" {
				t.Fatalf("%s izni dns:read olmalı: %s", name, spec.RequiredPerm)
			}
		case "dns.record.apply", "dns.record.delete":
			if spec.RequiredPerm != "dns:control" {
				t.Fatalf("%s izni dns:control olmalı: %s", name, spec.RequiredPerm)
			}
		case "cert.issue", "cert.renew", "cert.revoke":
			if spec.RequiredPerm != "cert:control" {
				t.Fatalf("%s izni cert:control olmalı: %s", name, spec.RequiredPerm)
			}
		case "web.cert.request", "web.cert.install":
			if spec.RequiredPerm != "web:control" {
				t.Fatalf("%s izni web:control olmalı: %s", name, spec.RequiredPerm)
			}
		case "web.cert.status":
			if spec.RequiredPerm != "web:read" {
				t.Fatalf("%s izni web:read olmalı: %s", name, spec.RequiredPerm)
			}
		}
	}
	// Control-plane hedefliler Exec taşımalı (registry invariant).
	for _, name := range []string{"dns.zone.list", "dns.record.list", "dns.record.diff", "dns.record.apply", "dns.record.delete", "cert.issue", "cert.renew", "cert.revoke"} {
		spec, _ := reg.Spec(name)
		if spec.Target != TargetControlPlane || spec.Exec == nil {
			t.Fatalf("%s control-plane hedefli + Exec olmalı", name)
		}
	}
	// Node hedefliler Exec'siz.
	for _, name := range []string{"web.cert.request", "web.cert.install", "web.cert.status"} {
		spec, _ := reg.Spec(name)
		if spec.Target != TargetNode || spec.Exec != nil {
			t.Fatalf("%s node hedefli olmalı", name)
		}
	}
}

func TestValidateDNSPayloads(t *testing.T) {
	validProvider := `{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666"}`
	if err := ValidateDNSProviderID([]byte(validProvider)); err != nil {
		t.Fatalf("geçerli provider payload: %v", err)
	}
	// UUID biçimi.
	if err := ValidateDNSProviderID([]byte(`{"provider_id":"not-a-uuid"}`)); err == nil {
		t.Fatal("geçersiz UUID reddedilmeli")
	}
	// Bilinmeyen alan (strict).
	if err := ValidateDNSProviderID([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","extra":1}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
	// Zone.
	if err := ValidateDNSZone([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com"}`)); err != nil {
		t.Fatalf("geçerli zone: %v", err)
	}
	if err := ValidateDNSZone([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com."}`)); err == nil {
		t.Fatal("sonda nokta reddedilmeli")
	}
	// Kayıt: geçerli A.
	rec := `{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com","name":"www","type":"A","value":"203.0.113.9","ttl":300}`
	if err := ValidateDNSRecord([]byte(rec)); err != nil {
		t.Fatalf("geçerli kayıt: %v", err)
	}
	// TTL aralığı dışı.
	if err := ValidateDNSRecord([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com","name":"www","type":"A","value":"203.0.113.9","ttl":10}`)); err == nil {
		t.Fatal("ttl 10 reddedilmeli")
	}
	// Bilinmeyen tür.
	if err := ValidateDNSRecord([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com","name":"www","type":"NS","value":"ns.example.com","ttl":300}`)); err == nil {
		t.Fatal("NS reddedilmeli (kapalı tür kümesi)")
	}
	// Değer biçimi.
	if err := ValidateDNSRecord([]byte(`{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com","name":"www","type":"A","value":"999.1.1.1","ttl":300}`)); err == nil {
		t.Fatal("999.1.1.1 reddedilmeli")
	}
	// Delete.
	del := `{"provider_id":"6f9a1c3e-1111-4222-8333-444455556666","zone":"example.com","name":"www","type":"A"}`
	if err := ValidateDNSRecordDelete([]byte(del)); err != nil {
		t.Fatalf("geçerli delete: %v", err)
	}
}

func TestValidateCertPayloads(t *testing.T) {
	csr := "-----BEGIN CERTIFICATE REQUEST-----\nMIIB\n-----END CERTIFICATE REQUEST-----"
	issue := map[string]any{
		"name": "example.internal", "provider_id": "6f9a1c3e-1111-4222-8333-444455556666", "csr_pem": csr,
	}
	b, _ := json.Marshal(issue)
	if err := ValidateCertIssue(b); err != nil {
		t.Fatalf("geçerli issue: %v", err)
	}
	// csr_pem boş.
	issue["csr_pem"] = ""
	b, _ = json.Marshal(issue)
	if err := ValidateCertIssue(b); err == nil {
		t.Fatal("boş csr reddedilmeli")
	}
	// csr_pem sınırı.
	issue["csr_pem"] = string(make([]byte, MaxCSRPEMLen+1))
	b, _ = json.Marshal(issue)
	if err := ValidateCertIssue(b); err == nil {
		t.Fatal("8KiB üstü csr reddedilmeli")
	}
	// Yanlış PEM türü.
	issue["csr_pem"] = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	b, _ = json.Marshal(issue)
	if err := ValidateCertIssue(b); err == nil {
		t.Fatal("CSR yerine sertifika reddedilmeli")
	}
	// Tekrarlı SAN.
	issue["csr_pem"] = csr
	issue["sans"] = []string{"example.internal", "www.example.internal", "www.example.internal"}
	b, _ = json.Marshal(issue)
	if err := ValidateCertIssue(b); err == nil {
		t.Fatal("tekrarlı SAN reddedilmeli")
	}
	// cert_id/renew.
	renew := `{"cert_id":"6f9a1c3e-1111-4222-8333-444455556666"}`
	if err := ValidateCertID([]byte(renew)); err != nil {
		t.Fatalf("geçerli renew: %v", err)
	}
	// reason sınırı (revoke).
	if err := ValidateCertID([]byte(`{"cert_id":"6f9a1c3e-1111-4222-8333-444455556666","reason":11}`)); err == nil {
		t.Fatal("reason 11 reddedilmeli")
	}
	if err := ValidateCertID([]byte(`{"cert_id":"6f9a1c3e-1111-4222-8333-444455556666","reason":5}`)); err != nil {
		t.Fatalf("reason 5 geçerli: %v", err)
	}
}

func TestValidateWebCertPayloads(t *testing.T) {
	req := `{"domain":"example.internal","sans":["www.example.internal"]}`
	if err := ValidateWebCertRequest([]byte(req)); err != nil {
		t.Fatalf("geçerli request: %v", err)
	}
	// Domain allowlist dışı.
	if err := ValidateWebCertRequest([]byte(`{"domain":"Bad!.internal"}`)); err == nil {
		t.Fatal("geçersiz domain reddedilmeli")
	}
	// Install.
	inst := `{"domain":"example.internal","cert_pem":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"}`
	if err := ValidateWebCertInstall([]byte(inst)); err != nil {
		t.Fatalf("geçerli install: %v", err)
	}
	if err := ValidateWebCertInstall([]byte(`{"domain":"example.internal","cert_pem":"değil"}`)); err == nil {
		t.Fatal("PEM olmayan cert_pem reddedilmeli")
	}
	// Status boş obje.
	if err := ValidateWebCertStatus([]byte(`{}`)); err != nil {
		t.Fatalf("boş obje: %v", err)
	}
	if err := ValidateWebCertStatus([]byte(`{"x":1}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
	if err := ValidateWebCertStatus(nil); err != nil {
		t.Fatalf("boş payload kabul: %v", err)
	}
}

func TestValidDNSDomainShape(t *testing.T) {
	for _, ok := range []string{"example.com", "a-b.example.internal", "9test.example"} {
		if !validDNSDomain(ok) {
			t.Fatalf("%s geçerli olmalı", ok)
		}
	}
	for _, bad := range []string{"", "-lead.example.com", "trail-.com", "Örnek.com", "double..dot", "spa ce.com", stringsRepeater()} {
		if validDNSDomain(bad) {
			t.Fatalf("%s reddedilmeli", bad)
		}
	}
}

func stringsRepeater() string {
	s := ""
	for i := 0; i < 255; i++ {
		s += "a"
	}
	return s + ".com"
}
