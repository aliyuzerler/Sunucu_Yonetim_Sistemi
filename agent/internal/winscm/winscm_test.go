//go:build windows

package winscm

import (
	"context"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// TestListSmoke exercises the REAL SCM read path on a Windows runner
// (unprivileged user can enumerate services).
func TestListSmoke(t *testing.T) {
	m := &Manager{Timeout: 10 * time.Second}
	services, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(services) == 0 {
		t.Fatal("Windows runner'da en az bir servis beklenir")
	}
	if len(services) > 500 {
		t.Fatalf("liste 500 ile sınırli: %d", len(services))
	}
	// Shape: her kayıt allowlist biçiminde bir KEY adı taşımalı.
	for _, s := range services {
		if err := winops.ValidateServiceName(s.Name); err != nil {
			t.Fatalf("liste çıktısı allowlist dışı: %q", s.Name)
		}
		if s.Status == "" {
			t.Fatalf("durum boş: %+v", s)
		}
	}
	t.Logf("listelendi: %d servis (ör. %s)", len(services), services[0].Name)
}

// TestStatusSmoke queries a service that ALWAYS exists on Windows.
func TestStatusSmoke(t *testing.T) {
	m := &Manager{Timeout: 10 * time.Second}
	si, err := m.Status(context.Background(), "Dhcp")
	if err != nil {
		t.Fatalf("Status(Dhcp): %v", err) // Dhcp: DHCP Client — her Windows'ta var
	}
	if si.Name != "Dhcp" {
		t.Fatalf("isim: %+v", si)
	}
	switch si.Status {
	case "running", "stopped", "start_pending", "stop_pending":
	default:
		t.Fatalf("beklenmeyen durum: %q", si.Status)
	}
	if si.StartUp == "unknown" {
		t.Logf("uyarı: StartType çözülemedi (runner kısıtı olabilir): %+v", si)
	}
}

// TestStatusRejectsInvalidName pins the allowlist BEFORE any SCM call.
func TestStatusRejectsInvalidName(t *testing.T) {
	m := &Manager{}
	for _, bad := range []string{"-flag", `..\evil`, "a|b", "trailing.", "trailing "} {
		if _, err := m.Status(context.Background(), bad); err == nil {
			t.Errorf("Status(%q) reddedilmeli", bad)
		}
	}
}
