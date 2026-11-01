package tasks

import (
	"encoding/json"
	"strings"
	"testing"
)

// Faz 04 payload validator unit tests (no PostgreSQL required).
func TestValidateServiceUnitPayload(t *testing.T) {
	valid := []string{"nginx.service", "user@1000.service", "php8.3-fpm.service", "systemd-fsck@dev-sda1.service"}
	for _, u := range valid {
		payload, _ := json.Marshal(map[string]string{"unit": u})
		if err := ValidateServiceUnit(payload); err != nil {
			t.Errorf("geçerli unit reddedildi: %s (%v)", u, err)
		}
	}
	invalid := map[string]string{
		"boş unit":        "",
		"flag enjeksiyon": "-bash.service",
		"yol geçişi":      "../etc/passwd.service",
		"shell meta":      "nginx;reboot.service",
		"komut ikamesi":   "nginx$(id).service",
		"backtick":        "nginx`id`.service",
		"boşluk":          "nginx .service",
		"yüzde":           "%x.service",
		"soneksiz":        "nginx",
	}
	for name, u := range invalid {
		payload, _ := json.Marshal(map[string]string{"unit": u})
		if err := ValidateServiceUnit(payload); err == nil {
			t.Errorf("geçersiz unit kabul edildi (%s): %q", name, u)
		}
	}
	// Şema ihlalleri.
	if err := ValidateServiceUnit(json.RawMessage(`{"unit":"a.service","extra":1}`)); err == nil {
		t.Fatal("bilinmeyen alan reddedilmeli")
	}
	if err := ValidateServiceUnit(json.RawMessage(`not-json`)); err == nil {
		t.Fatal("bozuk JSON reddedilmeli")
	}
	if err := ValidateServiceUnit(json.RawMessage(`{}`)); err == nil {
		t.Fatal("unit zorunlu")
	}
}

func TestValidateServiceListPayload(t *testing.T) {
	if err := ValidateServiceListPayload(nil); err != nil {
		t.Fatalf("boş payload kabul edilmeli: %v", err)
	}
	if err := ValidateServiceListPayload(json.RawMessage(`{}`)); err != nil {
		t.Fatalf("boş nesne kabul edilmeli: %v", err)
	}
	if err := ValidateServiceListPayload(json.RawMessage(`{"limit":200}`)); err != nil {
		t.Fatalf("limit kabul edilmeli: %v", err)
	}
	if err := ValidateServiceListPayload(json.RawMessage(`{"limit":501}`)); err == nil {
		t.Fatal("limit 501 reddedilmeli")
	}
	if err := ValidateServiceListPayload(json.RawMessage(`{"limit":-1}`)); err == nil {
		t.Fatal("negatif limit reddedilmeli")
	}
}

// RequiredPerm layering: service mutations require service:control, reads
// require service:read; echo remains unrestricted.
func TestServiceOpPermissionLayering(t *testing.T) {
	reg, err := BuildDefaultRegistry(&fakeTagSetter{}, Faz09Services{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"noop.echo":                     "",
		"agent.echo":                    "",
		"inventory.node.tag.set":        "",
		"linux.systemd.service.list":    PermServiceReadName,
		"linux.systemd.service.status":  PermServiceReadName,
		"linux.systemd.service.start":   PermServiceControlName,
		"linux.systemd.service.stop":    PermServiceControlName,
		"linux.systemd.service.restart": PermServiceControlName,
	}
	for op, want := range cases {
		spec, ok := reg.Spec(op)
		if !ok {
			t.Fatalf("%s kayıtlı olmalı", op)
		}
		if spec.RequiredPerm != want {
			t.Errorf("%s RequiredPerm = %q, beklenen %q", op, spec.RequiredPerm, want)
		}
	}
}

// Bound names used by tests without importing rbac here (avoid cycle-safe).
const (
	PermServiceReadName    = "service:read"
	PermServiceControlName = "service:control"
)

// Unit name regex must match the agent-side svcops allowlist (contract).
func TestUnitRegexMatchesAgentAllowlist(t *testing.T) {
	if !unitNameRE.MatchString("nginx.service") {
		t.Fatal("temel unit eşleşmeli")
	}
	if unitNameRE.MatchString(strings.Repeat("a", 300) + ".service") {
		t.Fatal("256 sınırı aşılmalı")
	}
}
