package fwops

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePreviewParams(t *testing.T) {
	valid := []PreviewParams{
		{Port: 8080, Protocol: "tcp", Action: ActionAllow},
		{Zone: "public", Port: 53, Protocol: "udp", Action: ActionRevoke},
		{Zone: "internal.lan-1", Port: 65535, Protocol: "tcp", Action: ActionAllow},
	}
	for _, p := range valid {
		if err := ValidatePreviewParams(p); err != nil {
			t.Errorf("geçerli params reddedildi: %+v → %v", p, err)
		}
	}
	invalid := []PreviewParams{
		{Port: 0, Protocol: "tcp", Action: ActionAllow},
		{Port: 65536, Protocol: "tcp", Action: ActionAllow},
		{Port: -1, Protocol: "tcp", Action: ActionAllow},
		{Port: 80, Protocol: "sctp", Action: ActionAllow},
		{Port: 80, Protocol: "", Action: ActionAllow},
		{Port: 80, Protocol: "tcp", Action: "drop"}, // closed action enum
		{Port: 80, Protocol: "tcp", Action: ActionAllow, Zone: "a;b"},
		{Port: 80, Protocol: "tcp", Action: ActionAllow, Zone: "zone;rm -rf"},
		{Port: 80, Protocol: "tcp", Action: ActionAllow, Zone: strings.Repeat("z", 33)},
	}
	for _, p := range invalid {
		if err := ValidatePreviewParams(p); !errors.Is(err, ErrInvalidPreview) {
			t.Errorf("geçersiz params kabul edildi: %+v → %v", p, err)
		}
	}
}

func TestRenderPreviewFirewalld(t *testing.T) {
	pv, err := RenderPreview(BackendFirewalld, PreviewParams{Zone: "public", Port: 8080, Protocol: "tcp", Action: ActionAllow})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if pv.Executed {
		t.Fatal("preview asla executed=true olamaz (ADR-0010)")
	}
	want := []string{
		"firewall-cmd --permanent --zone=public --add-port=8080/tcp",
		"firewall-cmd --reload",
	}
	if len(pv.Commands) != 2 || pv.Commands[0] != want[0] || pv.Commands[1] != want[1] {
		t.Fatalf("firewalld allow komutları: %+v", pv.Commands)
	}
	if pv.Note == "" || !strings.Contains(pv.Note, "ÖNİZLEME") {
		t.Fatalf("not: %q", pv.Note)
	}
	// revoke + default zone
	pv, err = RenderPreview(BackendFirewalld, PreviewParams{Port: 53, Protocol: "udp", Action: ActionRevoke})
	if err != nil || pv.Commands[0] != "firewall-cmd --permanent --zone=public --remove-port=53/udp" {
		t.Fatalf("revoke/default zone: %+v %v", pv, err)
	}
}

func TestRenderPreviewUfw(t *testing.T) {
	pv, err := RenderPreview(BackendUfw, PreviewParams{Port: 22, Protocol: "tcp", Action: ActionAllow})
	if err != nil || pv.Executed || pv.Commands[0] != "ufw allow 22/tcp" {
		t.Fatalf("ufw allow: %+v %v", pv, err)
	}
	pv, err = RenderPreview(BackendUfw, PreviewParams{Port: 22, Protocol: "tcp", Action: ActionRevoke})
	if err != nil || pv.Commands[0] != "ufw delete allow 22/tcp" {
		t.Fatalf("ufw revoke: %+v %v", pv, err)
	}
}

func TestRenderPreviewRejectsUnrenderableBackends(t *testing.T) {
	// nftables/iptables/none: preview deliberately unsupported (raw rule
	// syntax is host-specific; report-only in Faz 06 — linux-support.md).
	for _, b := range []string{BackendNft, BackendIptables, BackendNone, "unknown"} {
		if _, err := RenderPreview(b, PreviewParams{Port: 80, Protocol: "tcp", Action: ActionAllow}); !errors.Is(err, ErrPreviewUnsupported) {
			t.Errorf("backend %s için ErrPreviewUnsupported beklenir: %v", b, err)
		}
	}
}

func TestParseUfwActive(t *testing.T) {
	if !ParseUfwActive("Status: active\n") {
		t.Fatal("active beklenir")
	}
	if ParseUfwActive("Status: inactive\n") {
		t.Fatal("inactive beklenir")
	}
	if ParseUfwActive("") {
		t.Fatal("boş çıktı inactive")
	}
}

func TestParseActiveZones(t *testing.T) {
	out := "public\n  interfaces: ens3 ens4\ntrusted\nmalicious;zone\n\nother:thing\n"
	zones := ParseActiveZones(out)
	if len(zones) != 2 || zones[0] != "public" || zones[1] != "trusted" {
		t.Fatalf("zonelar: %+v", zones)
	}
}

func TestClampLines(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got, tr := ClampLines(lines, 2)
	if !tr || len(got) != 2 {
		t.Fatalf("clamp: %+v %v", got, tr)
	}
	got, tr = ClampLines(lines, 10)
	if tr || len(got) != 3 {
		t.Fatalf("sınır altı: %+v %v", got, tr)
	}
}
