package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/config"
)

func testHandler(ping Pinger) http.Handler {
	cfg, err := config.FromEnv(map[string]string{})
	if err != nil {
		panic(err)
	}
	return New(Options{Cfg: cfg, Logger: slog.New(slog.DiscardHandler), Pinger: ping})
}

// doGet performs a GET request and fails the test on transport errors.
func doGet(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("istek başarısız (%s): %v", path, err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("gövde okunamadı: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("JSON çözülemedi: %v (%q)", err, string(b))
	}
	return m
}

func TestHealthz(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return nil }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("istek başarısız: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Error("X-Request-ID başlığı eksik")
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("CSP başlığı eksik")
	}
	body := decodeBody(t, resp)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

func TestReadyzOk(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return nil }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/readyz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "ready" {
		t.Errorf("status = %v, want ready", body["status"])
	}
}

func TestReadyzDatabaseDown(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return errors.New("connection refused") }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/readyz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
	// Negatif güvenlik senaryosu: iç hata detayı sızmamalı.
	for _, m := range body {
		if s, ok := m.(string); ok && strings.Contains(s, "connection refused") {
			t.Fatalf("iç hata detayı yanıta sızdı: %q", s)
		}
	}
}

func TestReadyzNotConfigured(t *testing.T) {
	h := testHandler(nil) // pinger yok → not configured yolu
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/readyz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	comps, _ := body["components"].(map[string]any)
	if comps["database"] != "not_configured" {
		t.Errorf("database component = %v, want not_configured", comps["database"])
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return nil }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/yok")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", body["code"])
	}
	if body["correlation_id"] == "" || body["correlation_id"] == "unknown" {
		t.Errorf("correlation_id beklenirdi, got %v", body["correlation_id"])
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return nil }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("istek oluşturulamadı: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("istek başarısız: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestRequestIDAdoptionAndReject(t *testing.T) {
	h := testHandler(pingerFunc(func(context.Context) error { return nil }))
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("istek oluşturulamadı: %v", err)
	}
	req.Header.Set("X-Request-ID", "trace-123")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("istek başarısız: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-Request-ID"); got != "trace-123" {
		t.Errorf("geçerli ID benimsenmedi: %q", got)
	}

	req2, err := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("istek oluşturulamadı: %v", err)
	}
	req2.Header.Set("X-Request-ID", "<script>alert(1)</script>")
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("istek başarısız: %v", err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("X-Request-ID"); got == "<script>alert(1)</script>" {
		t.Error("güvensiz ID benimsenmemeliydi")
	}
}

func TestTimeoutEnvelope(t *testing.T) {
	slow := pingerFunc(func(ctx context.Context) error {
		time.Sleep(300 * time.Millisecond)
		return nil
	})
	cfg, _ := config.FromEnv(map[string]string{})
	cfg.WriteTimeout = 50 * time.Millisecond
	h := New(Options{Cfg: cfg, Logger: slog.New(slog.DiscardHandler), Pinger: slow})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp := doGet(t, srv, "/readyz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (timeout)", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["code"] != "timeout" {
		t.Errorf("code = %v, want timeout", body["code"])
	}
}
