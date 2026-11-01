//go:build linux

package webops

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// TestRealWebHostLifecycle exercises the FULL site lifecycle against the
// REAL host tools (nginx binary, systemctl, useradd) — it runs ONLY when
// EXPECTED_WEB=1 (CI-17 lesson: the gate must not silently skip; the CI job
// sets it and FAILS if the environment is missing). Requires root (useradd,
// /etc writes) and nginx installed; the disposable CI runner provides both.
// The document root of the test site is REMOVED by the test cleanup itself,
// never by production code (delete preserves it by design).
func TestRealWebHostLifecycle(t *testing.T) {
	if os.Getenv("EXPECTED_WEB") != "1" {
		t.Skip("EXPECTED_WEB=1 gerekli (gerçek host kanıtı; yerel koşu BLOCKED — CI kapsar)")
	}
	if os.Getuid() != 0 {
		t.Fatal("gerçek host testi root ister (useradd + /etc yazımı)")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Fatalf("nginx kurulu değil: %v", err)
	}
	const site = "srvctl-citest"
	r := NewRunner()
	ctx := context.Background()

	// Cleanup best-effort (test site yalnız disposable CI runner'da yaşar).
	t.Cleanup(func() {
		_, _ = r.Delete(ctx, site)
		_ = os.RemoveAll("/var/www/server-control/" + site)
	})

	spec := SiteSpec{
		Name: site, ServerNames: []string{"citest.example.internal"},
		Backend: BackendStatic, ListenPort: 18080,
	}
	res, err := r.Create(ctx, spec)
	if err != nil {
		t.Fatalf("gerçek create başarısız: %v", err)
	}
	if !res.Executed {
		t.Fatal("gerçek create Executed=true olmalı")
	}
	if _, err := os.Stat(res.ConfigPath); err != nil {
		t.Fatalf("config yazılmadı: %v", err)
	}
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		// systemd varsa reload gerçekten sinyallenmiş olmalı (hata yoktu).
		t.Logf("nginx reload sinyali verildi: %s", res.ConfigPath)
	}

	// Update: içerik değişir → rollback kopyası oluşur.
	spec.ServerNames = []string{"citest2.example.internal"}
	res2, err := r.Update(ctx, spec)
	if err != nil {
		t.Fatalf("gerçek update başarısız: %v", err)
	}
	if res2.RollbackPath == "" {
		t.Fatal("gerçek update rollback kopyası üretmeli")
	}
	if _, err := os.Stat(res2.RollbackPath); err != nil {
		t.Fatalf("rollback dosyası yok: %v", err)
	}

	// Disable + status.
	if _, err := r.Disable(ctx, site); err != nil {
		t.Fatalf("gerçek disable başarısız: %v", err)
	}
	st, err := r.Status(ctx, site)
	if err != nil || st.Site.Enabled {
		t.Fatalf("disable sonrası status: %+v err=%v", st, err)
	}

	// Delete: kullanıcı gider, docroot KORUNUR.
	res3, err := r.Delete(ctx, site)
	if err != nil {
		t.Fatalf("gerçek delete başarısız: %v", err)
	}
	if !res3.DocPreserved {
		t.Fatal("delete docroot'u korumalı (kriter + ADR-0012)")
	}
	if _, err := os.Stat(res3.DocRoot); err != nil {
		t.Fatalf("docroot korunmalı: %v", err)
	}
	if _, err := os.Stat("/var/www/server-control/" + site); err != nil {
		t.Fatalf("site kök dizini korunmalı: %v", err)
	}
}
