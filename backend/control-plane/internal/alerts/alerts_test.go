package alerts

import (
	"errors"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

func cpuRule() Rule {
	return Rule{
		ID: "11111111-1111-1111-1111-111111111111", Name: "CPU yüksek",
		Metric: MetricCPU, Op: OpGT, Threshold: 90, Severity: SevWarning, Enabled: true,
	}
}

func TestRuleValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Rule)
		wantErr bool
	}{
		{"geçerli", func(*Rule) {}, false},
		{"boş ad", func(r *Rule) { r.Name = "" }, true},
		{"kontrol karakterli ad", func(r *Rule) { r.Name = "bad\nname" }, true},
		{"bilinmeyen metrik", func(r *Rule) { r.Metric = "gpu" }, true},
		{"cpu dimension", func(r *Rule) { r.Dimension = "sda" }, true},
		{"disk dimension geçersiz", func(r *Rule) { r.Metric = MetricDisk; r.Dimension = "../etc" }, true},
		{"disk dimension geçerli", func(r *Rule) { r.Metric = MetricDisk; r.Dimension = "sda1" }, false},
		{"bilinmeyen op", func(r *Rule) { r.Op = "eq" }, true},
		{"eşik sıfır", func(r *Rule) { r.Threshold = 0 }, true},
		{"freshness düşük eşik", func(r *Rule) { r.Metric = MetricFreshness; r.Threshold = 30 }, true},
		{"freshness geçerli", func(r *Rule) { r.Metric = MetricFreshness; r.Threshold = 300 }, false},
		{"süre negatif", func(r *Rule) { r.DurationSeconds = -1 }, true},
		{"süre büyük", func(r *Rule) { r.DurationSeconds = 3601 }, true},
		{"severity geçersiz", func(r *Rule) { r.Severity = "urgent" }, true},
		{"node id geçersiz", func(r *Rule) { r.NodeID = "abc" }, true},
		{"node id uuid", func(r *Rule) { r.NodeID = "0b7f2f1e-1a2b-4c3d-8e9f-001122334455" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := cpuRule()
			tc.mutate(&r)
			err := r.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("hata bekleniyordu")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("beklenmeyen hata: %v", err)
			}
		})
	}
}

func TestEvaluateInstant(t *testing.T) {
	r := cpuRule()
	now := fixedNow()
	samples := []Sample{{Value: 95, CollectedAt: now.Add(-10 * time.Second)}}
	if v, _ := r.Evaluate(samples, now); !v {
		t.Fatalf("95 > 90 ihlal sayılmalı")
	}
	samples[0].Value = 50
	if v, _ := r.Evaluate(samples, now); v {
		t.Fatalf("50 ihlal sayılmamalı")
	}
	// Veri yok → ihlal yok (dürüst).
	if v, _ := r.Evaluate(nil, now); v {
		t.Fatalf("veri yokken ihlal sayılmamalı")
	}
}

func TestEvaluateSustainedWindow(t *testing.T) {
	r := cpuRule()
	r.DurationSeconds = 300
	now := fixedNow()

	// Pencere içindeki TÜM örnekler ihlal ediyor + geçmiş pencere dışına uzanıyor (tam gözlem).
	samples := []Sample{
		{Value: 95, CollectedAt: now.Add(-60 * time.Second)},
		{Value: 92, CollectedAt: now.Add(-180 * time.Second)},
		{Value: 91, CollectedAt: now.Add(-310 * time.Second)},
	}
	if v, worst := r.Evaluate(samples, now); !v || worst != 95 {
		t.Fatalf("sürekli ihlal beklendi; v=%v worst=%v", v, worst)
	}

	// Pencere içinde iyileşen örnek → ihlal yok.
	samples[1].Value = 40
	if v, _ := r.Evaluate(samples, now); v {
		t.Fatalf("pencere içinde iyileşen örnek ihlali bozmalı")
	}

	// Pencere başlangıcına uzanan geçmiş YOK → ihlal YOK (dürüst yetersiz veri).
	short := samples[:2]
	short[1].CollectedAt = now.Add(-100 * time.Second)
	if v, _ := r.Evaluate(short, now); v {
		t.Fatalf("pencere tam gözlemlenmeden ihlal söylenmemeli (dürüst yetersiz veri)")
	}
}

func TestAcknowledgeStateRules(t *testing.T) {
	now := fixedNow()
	a := Alert{Status: StatusOpen}
	if err := a.Acknowledge("admin", now); err != nil {
		t.Fatalf("open→acknowledged olmalı: %v", err)
	}
	if a.Status != StatusAcknowledged || a.AcknowledgedBy == nil || *a.AcknowledgedBy != "admin" {
		t.Fatalf("ack alanları set edilmeli")
	}
	firstAck := *a.AcknowledgedAt
	// İkinci ack idempotent (ilk zaman damgası korunur).
	if err := a.Acknowledge("operator", now.Add(time.Minute)); err != nil {
		t.Fatalf("tekrar ack idempotent olmalı: %v", err)
	}
	if !a.AcknowledgedAt.Equal(firstAck) {
		t.Fatalf("ilk ack zamanı korunmalı")
	}
	// Silenced → ack reddedilir.
	s := Alert{Status: StatusSilenced}
	if err := s.Acknowledge("admin", now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("silenced→acknowledged ErrInvalidState olmalı: %v", err)
	}
	// Resolved → ack reddedilir.
	res := Alert{Status: StatusResolved}
	if err := res.Acknowledge("admin", now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resolved→acknowledged ErrInvalidState olmalı: %v", err)
	}
}

func TestSilenceStateRules(t *testing.T) {
	now := fixedNow()
	a := Alert{Status: StatusOpen}
	until := now.Add(2 * time.Hour)
	if err := a.Silence("admin", until, now); err != nil {
		t.Fatalf("open→silenced olmalı: %v", err)
	}
	if a.Status != StatusSilenced || a.SilencedUntil == nil || !a.SilencedUntil.Equal(until) {
		t.Fatalf("silence alanları set edilmeli")
	}
	// Resolved → silence reddedilir.
	res := Alert{Status: StatusResolved}
	if err := res.Silence("admin", until, now); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resolved→silenced ErrInvalidState olmalı: %v", err)
	}
}

func TestResolveStateRules(t *testing.T) {
	now := fixedNow()
	a := Alert{Status: StatusSilenced}
	if err := a.Resolve("admin", now); err != nil {
		t.Fatalf("silenced→resolved olmalı: %v", err)
	}
	if a.Status != StatusResolved || a.ResolvedBy == nil || *a.ResolvedBy != "admin" {
		t.Fatalf("resolve alanları set edilmeli")
	}
	// Tekrar resolve no-op.
	if err := a.Resolve("admin", now); err != nil {
		t.Fatalf("resolved→resolved no-op olmalı: %v", err)
	}
}

func TestOnSilenceExpiry(t *testing.T) {
	now := fixedNow()
	// Süresi geçti + ihlal sürüyor → reopen.
	a := Alert{Status: StatusSilenced, SilencedUntil: ptrTime(now.Add(-time.Minute))}
	OnSilenceExpiry(&a, true, now)
	if a.Status != StatusOpen {
		t.Fatalf("ihlal sürerken sessizlik bitince reopen olmalı, got %s", a.Status)
	}
	// Süresi geçti + iyileşti → resolve.
	b := Alert{Status: StatusSilenced, SilencedUntil: ptrTime(now.Add(-time.Minute))}
	OnSilenceExpiry(&b, false, now)
	if b.Status != StatusResolved || b.ResolvedBy == nil || *b.ResolvedBy != "evaluator" {
		t.Fatalf("iyileşme sonrası evaluator ile resolve olmalı, got %s", b.Status)
	}
	// Süre henüz dolmadı → dokunma.
	c := Alert{Status: StatusSilenced, SilencedUntil: ptrTime(now.Add(time.Hour))}
	OnSilenceExpiry(&c, false, now)
	if c.Status != StatusSilenced {
		t.Fatalf("süresi dolmayan sessizlik dokunulmamalı")
	}
}

func TestFingerprintDedupe(t *testing.T) {
	r1 := cpuRule()
	r2 := cpuRule()
	r2.ID = "22222222-2222-2222-2222-222222222222"
	f1 := Fingerprint(r1.ID, "node-1", "")
	f2 := Fingerprint(r1.ID, "node-1", "")
	if f1 != f2 {
		t.Fatalf("aynı kural+node+dimension aynı parmak izi vermeli")
	}
	if Fingerprint(r1.ID, "node-2", "") == f1 {
		t.Fatalf("farklı node farklı parmak izi vermeli")
	}
	if Fingerprint(r2.ID, "node-1", "") == f1 {
		t.Fatalf("farklı kural farklı parmak izi vermeli")
	}
	if Fingerprint(r1.ID, "node-1", "sda") == f1 {
		t.Fatalf("farklı dimension farklı parmak izi vermeli")
	}
}

func TestRuleTitle(t *testing.T) {
	r := cpuRule()
	if got := RuleTitle(r); got != "CPU yüksek (tüm düğümler cpu)" {
		t.Fatalf("beklenmeyen başlık: %q", got)
	}
	r.NodeID = "0b7f2f1e-1a2b-4c3d-8e9f-001122334455"
	if got := RuleTitle(r); got != "CPU yüksek (düğüm cpu)" {
		t.Fatalf("beklenmeyen başlık: %q", got)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
