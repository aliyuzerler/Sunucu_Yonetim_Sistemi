package httpapi

// Faz 10 regresyon testleri:
//  1. Faz 09 DÜZELTMESİ: /dns-providers ve /certificates route'ları Faz 09
//     implementasyonunda server.go'ya hiç kaydedilmemişti (404) — bu test
//     route'ların ARTIK var olduğunu ve auth kapısının çalıştığını kanıtlar.
//  2. db-credentials uçları: auth gerektirir; yapılandırmasızsa 503
//     not_configured (dürüst degrade; tahmin yok).
//
// Not: bu testler auth'suz çağrı yapar → beklenen yanıt 401/403;
// rota VARLIĞI 404 olmamasıyla kanıtlanır (chi 404 vs auth reddi ayrımı).

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
)

func testHandlerFull(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.FromEnv(map[string]string{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return New(Options{Cfg: cfg, Logger: slog.New(slog.DiscardHandler)})
}

// Route varlığı: auth'suz istek 404 DEĞİL (401/403 auth katmanından) olmalı.
func TestFaz09RoutesRegisteredRegression(t *testing.T) {
	h := testHandlerFull(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	for _, path := range []string{"/api/v1/dns-providers", "/api/v1/certificates"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("istek başarısız %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("%s 404 — Faz 09 rota kaydı REGRESYONU (rota hâlâ kayıtsız!)", path)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s auth'suz 401 olmalı: %d", path, resp.StatusCode)
		}
	}
}

// db-credentials uçları: auth kapısı + yapılandırmasız servis dürüst 503.
func TestDBCredentialEndpointsAuthGated(t *testing.T) {
	h := testHandlerFull(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	node := "0aa1c3e1-1111-4222-8333-444455556666"
	resp, err := srv.Client().Get(srv.URL + "/api/v1/nodes/" + node + "/db-credentials")
	if err != nil {
		t.Fatalf("liste isteği: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("db-credentials liste rotası kayıtsız")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("auth'suz liste 401 olmalı: %d", resp.StatusCode)
	}

	// Reveal: auth'suz POST → 401 (rota var; CSRF/auth katmanı reddeder).
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/api/v1/db-credentials/11111111-2222-4333-8444-555566667777/reveal", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("reveal isteği: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusNotFound {
		t.Fatal("db-credentials reveal rotası kayıtsız")
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("auth'suz reveal 401 olmalı: %d", resp2.StatusCode)
	}
}

// Faz 13: alarm/güvenlik/metrik route'ları kayıtlı olmalı (Faz 09 dersi:
// kayıtsız route 404 döner ve mock mod e2e bunu maskeler).
func TestFaz13RoutesRegisteredRegression(t *testing.T) {
	h := testHandlerFull(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	authPaths := []string{
		"/api/v1/alert-rules",
		"/api/v1/alerts",
		"/api/v1/security/overview",
	}
	for _, path := range authPaths {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("istek başarısız %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("%s 404 — Faz 13 rota kaydı REGRESYONU", path)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s auth'suz 401 olmalı: %d", path, resp.StatusCode)
		}
	}

	// /metrics auth'suzdur (scrape yüzeyi) — 404 DEĞİL, 200 veya 401(token
	// ayarlıysa) dönmeli; yapılandırmasızsa dürüst 503.
	resp, err := srv.Client().Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics isteği başarısız: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("/metrics 404 — Faz 13 rota kaydı REGRESYONU")
	}
}
