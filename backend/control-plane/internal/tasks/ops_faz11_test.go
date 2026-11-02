package tasks

// Faz 11 validator testleri: mail.* payload şemaları agent/internal/mailops
// ile AYNA'dır; her validatörün olumlu/olumsuz yolları burada güvenceye
// alınır (ADR-0015: payload'lar iki tarafta da strict doğrulanır).

import (
	"strings"
	"testing"
)

func TestMailValidDomain(t *testing.T) {
	for _, d := range []string{"example.com", "mail.example.com", "a-b.co"} {
		if !mailValidDomain(d) {
			t.Errorf("mailValidDomain(%q) = false, want true", d)
		}
	}
	for _, d := range []string{"", "Example.com", "example", "-a.b", "a-.b", "a_b.com", "a..b.com", "ex ample.com"} {
		if mailValidDomain(d) {
			t.Errorf("mailValidDomain(%q) = true, want false", d)
		}
	}
}

func TestMailValidLocalAddress(t *testing.T) {
	for _, l := range []string{"a", "user1", "first.last", "a-b_c"} {
		if !mailValidLocal(l) {
			t.Errorf("mailValidLocal(%q) = false, want true", l)
		}
	}
	for _, l := range []string{"", ".a", "a.", "a..b", "-a", "A", "a b"} {
		if mailValidLocal(l) {
			t.Errorf("mailValidLocal(%q) = true, want false", l)
		}
	}
	if !mailValidAddress("user@example.com") {
		t.Error("user@example.com valid olmalı")
	}
	for _, a := range []string{"userexample.com", "@example.com", "user@", "Us er@x.com"} {
		if mailValidAddress(a) {
			t.Errorf("mailValidAddress(%q) = true, want false", a)
		}
	}
}

func TestMailValidQuotaAndLimits(t *testing.T) {
	if !mailValidQuota(int64(1) << 30) {
		t.Error("1 GiB geçerli olmalı")
	}
	if mailValidQuota(int64(1<<20) - 1) {
		t.Error("min altı kota geçersiz olmalı")
	}
	if mailValidQuota((int64(1) << 40) + 1) {
		t.Error("max üstü kota geçersiz olmalı")
	}
	if !mailValidLimitKey("message_size_limit", 10<<20) {
		t.Error("message_size_limit 10 MiB geçerli olmalı")
	}
	if mailValidLimitKey("message_size_limit", 200<<20) {
		t.Error("aralık dışı değer geçersiz olmalı")
	}
	if mailValidLimitKey("evil_key", 1) {
		t.Error("kapalı enum dışı anahtar geçersiz olmalı")
	}
}

func TestValidateMailDomainPayload(t *testing.T) {
	if err := validateMailDomain([]byte(`{"domain":"example.com"}`)); err != nil {
		t.Fatalf("geçerli domain reddedildi: %v", err)
	}
	for _, body := range []string{
		`{"domain":"Example.com"}`,
		`{"domain":""}`,
		`{"domain":"example.com","extra":1}`, // unknown field
		`{}`,
		`not-json`,
	} {
		if err := validateMailDomain([]byte(body)); err == nil {
			t.Errorf("bozuk payload reddedilmeli: %s", body)
		}
	}
}

func TestValidateMailBoxCreatePayload(t *testing.T) {
	if err := validateMailBoxCreate([]byte(`{"domain":"example.com","local":"user"}`)); err != nil {
		t.Fatalf("geçerli mailbox reddedildi: %v", err)
	}
	for _, body := range []string{
		`{"domain":"Example.com","local":"user"}`,
		`{"domain":"example.com","local":".user"}`,
		`{"domain":"example.com"}`,
		`{"domain":"example.com","local":"user","quota":1}`,
	} {
		if err := validateMailBoxCreate([]byte(body)); err == nil {
			t.Errorf("bozuk payload reddedilmeli: %s", body)
		}
	}
}

func TestValidateMailBoxDeletePayload(t *testing.T) {
	if err := validateMailBoxDelete([]byte(`{"mailbox":"user@example.com"}`)); err != nil {
		t.Fatalf("geçerli silme reddedildi: %v", err)
	}
	if err := validateMailBoxDelete([]byte(`{"mailbox":"not-an-address"}`)); err == nil {
		t.Fatal("adres dışı payload reddedilmeli")
	}
}

func TestValidateMailAliasPayloads(t *testing.T) {
	if err := validateMailAliasCreate([]byte(`{"alias":"a@example.com","target":"user@example.com"}`)); err != nil {
		t.Fatalf("geçerli alias reddedildi: %v", err)
	}
	// kendine alias: tipli red
	if err := validateMailAliasCreate([]byte(`{"alias":"a@example.com","target":"a@example.com"}`)); err == nil {
		t.Fatal("kendine alias reddedilmeli")
	}
	// hedef allowlist dışı
	if err := validateMailAliasCreate([]byte(`{"alias":"a@example.com","target":"BAD"}`)); err == nil {
		t.Fatal("bozuk hedef reddedilmeli")
	}
	if err := validateMailAliasDelete([]byte(`{"alias":"a@example.com"}`)); err != nil {
		t.Fatalf("geçerli alias silme reddedildi: %v", err)
	}
}

func TestValidateMailQuotaPayload(t *testing.T) {
	if err := validateMailQuota([]byte(`{"mailbox":"user@example.com","bytes":536870912}`)); err != nil {
		t.Fatalf("geçerli kota reddedildi: %v", err)
	}
	if err := validateMailQuota([]byte(`{"mailbox":"user@example.com","bytes":1024}`)); err == nil {
		t.Fatal("min altı kota reddedilmeli")
	}
	if err := validateMailQuota([]byte(`{"mailbox":"ghost@","bytes":536870912}`)); err == nil {
		t.Fatal("bozuk adres reddedilmeli")
	}
}

func TestValidateMailLimitsPayload(t *testing.T) {
	ok := `{"limits":{"message_size_limit":20971520,"anvil_rate_time_unit":300}}`
	if err := validateMailLimits([]byte(ok)); err != nil {
		t.Fatalf("geçerli limits reddedildi: %v", err)
	}
	for _, body := range []string{
		`{"limits":{}}`,         // boş
		`{"limits":{"evil":1}}`, // kapalı enum dışı
		`{"limits":{"message_size_limit":999999999999}}`, // aralık dışı
		`{}`, // limits zorunlu
	} {
		if err := validateMailLimits([]byte(body)); err == nil {
			t.Errorf("bozuk limits reddedilmeli: %s", body)
		}
	}
}

func TestValidateMailQueuePayload(t *testing.T) {
	if err := validateMailQueue(nil); err != nil {
		t.Fatalf("boş payload kabul edilmeli: %v", err)
	}
	if err := validateMailQueue([]byte(`{"limit":200}`)); err != nil {
		t.Fatalf("geçerli limit reddedildi: %v", err)
	}
	if err := validateMailQueue([]byte(`{"limit":201}`)); err == nil {
		t.Fatal("200 üstü limit reddedilmeli (kriter 5 sınırlı görüntü)")
	}
}

func TestBuildFaz11RegistryShapes(t *testing.T) {
	specs := buildFaz11Registry()
	if len(specs) != 16 {
		t.Fatalf("16 spec bekleniyordu: %d", len(specs))
	}
	byName := map[string]*OperationSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	// Tümü node hedefli ve mail izinli olmalı.
	for name, s := range byName {
		if s.Target != TargetNode {
			t.Errorf("%s hedefi node olmalı", name)
		}
		if s.RequiredPerm != "mail:read" && s.RequiredPerm != "mail:control" {
			t.Errorf("%s izni mail:* olmalı: %q", name, s.RequiredPerm)
		}
		if strings.Contains(name, ".list") || strings.HasSuffix(name, ".preflight") ||
			name == "mail.status" || name == "mail.config.check" || name == "mail.limits.get" {
			if s.RequiredPerm != "mail:read" {
				t.Errorf("%s salt-okuma olmalı (mail:read): %q", name, s.RequiredPerm)
			}
		}
	}
	// Mutasyon yüzeyi mail:control olmalı.
	for _, name := range []string{"mail.domain.create", "mail.domain.delete", "mail.mailbox.create",
		"mail.mailbox.delete", "mail.alias.create", "mail.alias.delete", "mail.quota.set", "mail.limits.set"} {
		if byName[name].RequiredPerm != "mail:control" {
			t.Errorf("%s mail:control olmalı", name)
		}
	}
	// delete ailesi retry-güvensiz + destructive olmalı (db deseni).
	for _, name := range []string{"mail.domain.delete", "mail.mailbox.delete", "mail.alias.delete"} {
		if byName[name].Class != ClassDestructive || byName[name].RetrySafe {
			t.Errorf("%s destructive + retry-güvensiz olmalı", name)
		}
	}
}
