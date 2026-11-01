//go:build windows

package winevent

import (
	"context"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
)

// TestReadSmoke exercises the REAL wevtutil read path on a Windows runner.
// The System channel is readable by unprivileged users.
func TestReadSmoke(t *testing.T) {
	r := &Reader{Timeout: 15 * time.Second}
	events, err := r.Read(context.Background(), winops.EventlogParams{Channel: "System", Count: 10})
	if err != nil {
		t.Fatalf("Read(System): %v", err)
	}
	if len(events) == 0 {
		t.Fatal("System kanalında en az bir olay beklenir (runner'da boş olamaz)")
	}
	if len(events) > 10 {
		t.Fatalf("count sınırı aşıldı: %d", len(events))
	}
	for _, e := range events {
		if e.TimeCreated == "" || e.Provider == "" {
			t.Fatalf("olay alanları eksik: %+v", e)
		}
		if e.Level < 1 || e.Level > 5 {
			t.Fatalf("seviye sınırlı: %+v", e)
		}
		if len(e.Message) > 4000 {
			t.Fatalf("mesaj sınırı: %d", len(e.Message))
		}
	}
	t.Logf("okundu: %d olay (ör. EventID=%d, provider=%s)", len(events), events[0].EventID, events[0].Provider)
}

// TestReadRejectsSecurityChannel pins the ADR-0009 exclusion.
func TestReadRejectsSecurityChannel(t *testing.T) {
	r := &Reader{}
	if _, err := r.Read(context.Background(), winops.EventlogParams{Channel: "Security"}); err == nil {
		t.Fatal("Security kanalı reddedilmeli (ADR-0009)")
	}
}

// TestProbeAvailableSmoke checks the capability probe end-to-end.
func TestProbeAvailableSmoke(t *testing.T) {
	r := &Reader{}
	ok, reason := r.ProbeAvailable(context.Background())
	if !ok {
		t.Fatalf("System kanalı sondası başarısız: %s", reason)
	}
}
