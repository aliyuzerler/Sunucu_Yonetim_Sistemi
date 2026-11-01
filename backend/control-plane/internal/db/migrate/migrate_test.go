package migrate

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to the integration database when TEST_DATABASE_URL is set.
// CI provides a disposable PostgreSQL 18 container for this test; locally the
// test is skipped with an explicit message (never silently passed).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL tanımsız; migration entegrasyon testi atlandı (CI'da PostgreSQL 18 ile çalışır)")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("test veritabanına bağlanılamadı: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func pairsForTest() []Pair {
	return []Pair{
		{
			Version: 1,
			Name:    "baseline",
			UpSQL: `CREATE TABLE IF NOT EXISTS platform_metadata (
                                                key TEXT PRIMARY KEY, value JSONB NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now());`,
			DownSQL: `DROP TABLE IF EXISTS platform_metadata;`,
		},
		{
			Version: 2,
			Name:    "dummy",
			UpSQL:   `CREATE TABLE IF NOT EXISTS migration_dummy (id INTEGER PRIMARY KEY);`,
			DownSQL: `DROP TABLE IF EXISTS migration_dummy;`,
		},
	}
}

func TestMigrationUpDownIntegration(t *testing.T) {
	pool := testPool(t)
	r := &Runner{Pool: pool, Migrations: pairsForTest()}
	ctx := context.Background()

	applied, err := r.Up(ctx)
	if err != nil {
		t.Fatalf("Up başarısız: %v", err)
	}
	if applied != 2 {
		t.Fatalf("uygulanan migration sayısı = %d, want 2", applied)
	}

	// Idempotency: ikinci Up hiçbir şey uygulamamalı.
	if applied, _ = r.Up(ctx); applied != 0 {
		t.Fatalf("idempotent Up beklenirdi, got %d", applied)
	}

	status, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("Status başarısız: %v", err)
	}
	for _, s := range status {
		if !s.Applied {
			t.Errorf("migration %d uygulanmamış görünüyor", s.Version)
		}
	}

	if reverted, err := r.DownOne(ctx); err != nil || !reverted {
		t.Fatalf("DownOne başarısız: reverted=%v err=%v", reverted, err)
	}
	status, _ = r.Status(ctx)
	if status[1].Applied {
		t.Error("migration 2 down sonrası applied olmamalı")
	}

	// Temizlik: baseline'ı da geri al ki test tekrar çalışabilsin.
	if reverted, err := r.DownOne(ctx); err != nil || !reverted {
		t.Fatalf("DownOne (baseline) başarısız: reverted=%v err=%v", reverted, err)
	}
}

func TestAdvisoryLockRejectsSecondRunnerIntegration(t *testing.T) {
	pool := testPool(t)
	r2 := &Runner{Pool: pool, Migrations: pairsForTest()}
	ctx := context.Background()

	// Harici bir runner kilidi tutuyormuş gibi kilidi doğrudan al.
	// Not: release yalnızca unlock'tan sonra olmalı; her ikisi de t.Cleanup'ta
	// yapılır (defer'ler cleanup'lardan önce koşar — bkz. CI bulgusu 2026-11-01).
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("bağlantı alınamadı: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
		conn.Release()
	})

	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&ok); err != nil {
		t.Fatalf("kilit sorgusu başarısız: %v", err)
	}
	if !ok {
		t.Fatal("test kilidi alınamadı")
	}

	if _, err := r2.Up(ctx); err == nil {
		t.Fatal("kilit tutulurken ikinci runner başarısız olmalıydı")
	}
}
