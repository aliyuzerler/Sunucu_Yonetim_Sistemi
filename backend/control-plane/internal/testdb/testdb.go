// Package testdb provides the shared PostgreSQL test harness. Tests skip
// unless TEST_DATABASE_URL points at a disposable database (CI provisions
// PostgreSQL 18 as a service container; locally use deploy/docker-compose).
// Each test PROCESS (package) gets its own uniquely-named database so
// parallel packages never share schema state; the disposable databases die
// with the CI job and can be dropped locally at will.
package testdb

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/db/migrations"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db/migrate"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URL returns the configured test database URL ("" when unset).
func URL() string { return os.Getenv("TEST_DATABASE_URL") }

var (
	dbOnce  sync.Once
	dbURL   string
	dbSetup error
)

// Pool connects to this package's isolated database (one per test process,
// via testutil.UniqueDB) and applies all migrations. Skips the calling test
// when TEST_DATABASE_URL is not configured.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL := URL()
	if adminURL == "" {
		t.Skip("TEST_DATABASE_URL tanımlı değil; PostgreSQL entegrasyon testi atlandı")
	}
	dbOnce.Do(func() {
		dbURL = testutil.UniqueDBPersistent(t, adminURL)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("test veritabanı URL'si geçersiz: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("test havuzu: %v", err)
	}
	t.Cleanup(pool.Close)

	runner := &migrate.Runner{Pool: pool, Migrations: mustMigrations(t)}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("migration'lar uygulanamadı: %v", err)
	}
	return pool
}

func mustMigrations(t *testing.T) []migrate.Pair {
	t.Helper()
	src, err := migrations.All()
	if err != nil {
		t.Fatalf("migration çiftleri okunamadı: %v", err)
	}
	out := make([]migrate.Pair, 0, len(src))
	for _, p := range src {
		out = append(out, migrate.Pair{Version: p.Version, Name: p.Name, UpSQL: p.UpSQL, DownSQL: p.DownSQL})
	}
	return out
}

// Reset truncates all product tables between tests. TRUNCATE does not fire
// the row-level DELETE trigger, so the append-only audit table clears too.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `
TRUNCATE audit_events, outbox_events, task_attempts, tasks,
         node_capabilities, node_facts, node_tags, node_group_members,
         node_metrics, node_system_facts,
         nodes, node_groups, sessions, admin_bootstrap_tokens,
         user_role_bindings, users
RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("test veritabanı sıfırlanamadı: %v", err)
	}
}

// MustExec runs a statement and fails the test on error.
func MustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("sorgu başarısız: %v (%s)", err, sql)
	}
}

// QueryInt runs a single-value query returning an integer.
func QueryInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("sayım sorgusu başarısız: %v (%s)", err, sql)
	}
	return n
}
