package audit

import (
	"context"
	"testing"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

// TestAppendOnlyPG: kayıt yazılır; UPDATE ve DELETE tetikleyiciyle engellenir.
func TestAppendOnlyPG(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx := context.Background()

	rec := NewPGRecorder(pool)
	ev := Event{
		Actor:         "admin",
		Action:        "node.create",
		TargetType:    "node",
		TargetID:      "n-1",
		Result:        ResultSuccess,
		CorrelationID: "corr-a-1",
		RequestID:     "req-1",
		Metadata: map[string]any{
			"display_name": "web-01",
			"password":     "sızmasın",
		},
	}
	if err := rec.Record(ctx, ev); err != nil {
		t.Fatalf("kayıt yazılamadı: %v", err)
	}

	page, err := Query(ctx, pool, Filter{Action: "node.create"})
	if err != nil {
		t.Fatalf("sorgu: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("bir kayıt bekleniyordu: %d", len(page.Entries))
	}
	e := page.Entries[0]
	if e.Actor != "admin" || e.TargetID != "n-1" || e.CorrelationID != "corr-a-1" {
		t.Fatalf("alanlar korunmalı: %+v", e)
	}
	// Redaction DB katmanına kadar taşınmalı.
	if pw, ok := e.Metadata["password"].(string); !ok || pw == "sızmasın" {
		t.Fatalf("secret redaction: %+v", e.Metadata)
	}

	// UPDATE engellenir.
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET actor = 'kötü'`); err == nil {
		t.Fatal("audit UPDATE engellenmeli")
	}
	// DELETE engellenir.
	if _, err := pool.Exec(ctx, `DELETE FROM audit_events`); err == nil {
		t.Fatal("audit DELETE engellenmeli")
	}
}

// TestQueryPaginationPG: keyset imleç sayfalama.
func TestQueryPaginationPG(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx := context.Background()

	rec := NewPGRecorder(pool)
	for i := 0; i < 7; i++ {
		action := "node.tag"
		if i%2 == 0 {
			action = "node.create"
		}
		if err := rec.Record(ctx, Event{
			Actor: "admin", Action: action, Result: ResultSuccess,
			CorrelationID: "corr-p", Metadata: map[string]any{"i": i},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Limit 3 → 3 kayıt + imleç.
	page1, err := Query(ctx, pool, Filter{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Entries) != 3 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("ilk sayfa: %d more=%v", len(page1.Entries), page1.HasMore)
	}
	page2, err := Query(ctx, pool, Filter{Limit: 3, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Entries) != 3 {
		t.Fatalf("ikinci sayfa: %d", len(page2.Entries))
	}
	if page2.Entries[0].ID >= page1.Entries[0].ID {
		t.Fatal("id azalan sırada olmalı")
	}
	// Filtre.
	pageA, err := Query(ctx, pool, Filter{Action: "node.create", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(pageA.Entries) != 4 {
		t.Fatalf("action filtresi: %d", len(pageA.Entries))
	}
	// Geçersiz imleç reddedilir.
	if _, err := Query(ctx, pool, Filter{Cursor: "!!!"}); err == nil {
		t.Fatal("geçersiz imleç reddedilmeli")
	}
	// Doğrulama hatası: geçersiz action reddedilir.
	if err := rec.Record(ctx, Event{Actor: "x", Action: "KÖTÜ", Result: ResultSuccess, CorrelationID: "c"}); err == nil {
		t.Fatal("geçersiz action reddedilmeli")
	}
}
