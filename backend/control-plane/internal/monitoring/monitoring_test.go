package monitoring

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestRenderCounterAndGaugeDeterministic(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Counter("sc_test_total", "test sayacı", "route", "code"); err != nil {
		t.Fatalf("counter kaydı: %v", err)
	}
	if err := reg.Gauge("sc_test_gauge", "test göstergesi", "node"); err != nil {
		t.Fatalf("gauge kaydı: %v", err)
	}
	if err := reg.IncCounter("sc_test_total", []string{"/api/v1/x", "200"}, 2); err != nil {
		t.Fatalf("inc: %v", err)
	}
	if err := reg.IncCounter("sc_test_total", []string{"/api/v1/x", "500"}, 1); err != nil {
		t.Fatalf("inc2: %v", err)
	}
	if err := reg.SetGauge("sc_test_gauge", []string{"abc"}, 12.5); err != nil {
		t.Fatalf("set: %v", err)
	}

	out := reg.Render()
	if out != reg.Render() {
		t.Fatalf("render deterministik değil")
	}
	// Satır sırası label değerlerine göre sıralanmış olmalı (500 < 200 değildir;
	// "200" < "500" string olarak — sıralı çıktıda 200 önce gelir).
	i200 := strings.Index(out, `code="200"`)
	i500 := strings.Index(out, `code="500"`)
	if i200 < 0 || i500 < 0 || i200 > i500 {
		t.Fatalf("seri sıralaması bozuk:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE sc_test_total counter") ||
		!strings.Contains(out, "# TYPE sc_test_gauge gauge") {
		t.Fatalf("HELP/TYPE satırları eksik:\n%s", out)
	}
	if !strings.Contains(out, `sc_test_gauge{node="abc"} 12.5`) {
		t.Fatalf("gauge değeri hatalı:\n%s", out)
	}
}

func TestRenderGaugeWithTimestamp(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Gauge("sc_ts_gauge", "ts'li gösterge", "node"); err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	ts := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC).UnixMilli()
	if err := reg.SetGaugeAt("sc_ts_gauge", []string{"n1"}, 42, ts); err != nil {
		t.Fatalf("set: %v", err)
	}
	out := reg.Render()
	want := `sc_ts_gauge{node="n1"} 42 ` + strconv.FormatInt(ts, 10)
	if !strings.Contains(out, want) {
		t.Fatalf("timestamp'lı satır yok (beklenen %q):\n%s", want, out)
	}
	// ts=0 satırı timestamp'siz olmalı.
	if err := reg.SetGauge("sc_ts_gauge", []string{"n2"}, 7); err != nil {
		t.Fatalf("set2: %v", err)
	}
	out = reg.Render()
	if strings.Contains(out, `sc_ts_gauge{node="n2"} 7 `) {
		t.Fatalf("ts'siz satır timestamp taşımamalı:\n%s", out)
	}
}

func TestLabelEscape(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Gauge("sc_esc", "escape testi", "q"); err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	if err := reg.SetGauge("sc_esc", []string{"a\"b\\c\nd"}, 1); err != nil {
		t.Fatalf("set: %v", err)
	}
	out := reg.Render()
	if !strings.Contains(out, `q="a\"b\\c\nd"`) {
		t.Fatalf("escape hatalı:\n%s", out)
	}
}

func TestCardinalityBound(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Counter("sc_bound_total", "sınır testi", "k"); err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	for i := 0; i < maxSeriesPerFamily; i++ {
		if err := reg.IncCounter("sc_bound_total", []string{strconv.Itoa(i)}, 1); err != nil {
			t.Fatalf("sınır altındaki seri reddedildi: %v", err)
		}
	}
	if err := reg.IncCounter("sc_bound_total", []string{"overflow"}, 1); !errors.Is(err, ErrCardinalityBound) {
		t.Fatalf("sınır aşımı ErrCardinalityBound olmalı: %v", err)
	}
}

func TestInvalidNamesRejected(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Counter("1bad", "x"); err == nil {
		t.Fatalf("geçersiz metrik adı kabul edildi")
	}
	if err := reg.Counter("ok_name", "x", "bad-label"); err == nil {
		t.Fatalf("geçersiz label adı kabul edildi")
	}
	if err := reg.SetGauge("sc_unknow", nil, 1); err == nil {
		t.Fatalf("kayıtsız gauge kabul edildi")
	}
}

func TestHistogramRender(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Histogram("sc_hist_seconds", "hist testi", []float64{0.1, 1}, "route"); err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	if err := reg.Observe("sc_hist_seconds", []string{"/x"}, 0.05); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := reg.Observe("sc_hist_seconds", []string{"/x"}, 2); err != nil {
		t.Fatalf("observe2: %v", err)
	}
	out := reg.Render()
	wants := []string{
		`sc_hist_seconds_bucket{route="/x",le="0.1"} 1`,
		`sc_hist_seconds_bucket{route="/x",le="1"} 1`,
		`sc_hist_seconds_bucket{route="/x",le="+Inf"} 2`,
		`sc_hist_seconds_count{route="/x"} 2`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("eksik satır %q:\n%s", w, out)
		}
	}
}

func TestHTTPMiddlewareRoutePatternNotURL(t *testing.T) {
	reg := NewRegistry()
	obs, err := NewHTTPObserver(reg)
	if err != nil {
		t.Fatalf("observer: %v", err)
	}

	r := chi.NewRouter()
	r.Use(obs.Middleware)
	r.Get("/api/v1/nodes/{nodeID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Node ID kullanıcının URL'inde — ama label'da PATTERN olmalı.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/VERY-SECRET-NODE-ID", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := reg.Render()
	if strings.Contains(out, "VERY-SECRET-NODE-ID") {
		t.Fatalf("kullanıcı girdisi label'a sızmış:\n%s", out)
	}
	if !strings.Contains(out, `route="/api/v1/nodes/{nodeID}"`) {
		t.Fatalf("route pattern label'ı yok:\n%s", out)
	}
	if !strings.Contains(out, `method="GET",code="200"`) {
		t.Fatalf("method/code label'ları yok:\n%s", out)
	}
}

func TestHTTPMiddlewareUnmatched(t *testing.T) {
	reg := NewRegistry()
	obs, err := NewHTTPObserver(reg)
	if err != nil {
		t.Fatalf("observer: %v", err)
	}
	r := chi.NewRouter()
	r.Use(obs.Middleware)
	r.Get("/known", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	req := httptest.NewRequest(http.MethodGet, "/no-such-route", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := reg.Render()
	if !strings.Contains(out, `route="unmatched"`) {
		t.Fatalf("unmatched route label'ı yok:\n%s", out)
	}
}
