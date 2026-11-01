// Package testutil provides tiny, dependency-free test helpers. It must stay
// a leaf package so test files of packages it touches can import it without
// import cycles.
package testutil

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminURL returns TEST_DATABASE_URL ("" when unset).
func AdminURL() string { return os.Getenv("TEST_DATABASE_URL") }

// UniqueDB creates a uniquely-named disposable database from the admin URL
// and returns its URL. One database per test PROCESS keeps parallel packages
// from sharing schema state (CI finding CI-1/CI-4, 2026-11-01). The database
// is dropped (WITH FORCE) when the test ends, then the admin pool closes.
func UniqueDB(t *testing.T, adminURL string) string {
	t.Helper()
	admin, err := pgxpool.New(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("admin bağlantısı: %v", err)
	}

	name := fmt.Sprintf("srvctl_test_%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	if _, err := admin.Exec(context.Background(),
		fmt.Sprintf(`CREATE DATABASE %s`, name)); err != nil {
		admin.Close()
		t.Fatalf("test veritabanı oluşturulamadı (%s): %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
		admin.Close()
	})
	return ReplaceDBName(adminURL, name)
}

// ReplaceDBName swaps the database component of a PostgreSQL URL.
func ReplaceDBName(url, name string) string {
	// postgres://user:pass@host:port/dbname?params → .../name?params
	if i := strings.Index(url, "://"); i >= 0 {
		rest := url[i+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			path := rest[slash+1:]
			q := ""
			if qi := strings.Index(path, "?"); qi >= 0 {
				q = path[qi:]
				path = path[:qi]
			}
			if path != "" {
				return url[:i+3] + rest[:slash] + "/" + name + q
			}
		}
	}
	return url + "/" + name
}
