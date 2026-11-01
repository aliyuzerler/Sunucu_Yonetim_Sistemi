// Package migrate implements the explicit SQL migration runner.
//
// Design (backend-rules): migrations are explicit, transactional, reversible
// when safe and guarded by a PostgreSQL advisory lock so two control plane
// instances never run them concurrently.
package migrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is an arbitrary but fixed key reserved for schema migrations.
const advisoryLockKey = int64(748001)

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    BIGINT PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Pair is one versioned migration (up/down SQL).
type Pair struct {
	Version int
	Name    string
	UpSQL   string
	DownSQL string
}

// Status describes a migration's application state.
type Status struct {
	Version int
	Name    string
	Applied bool
}

// Runner applies migration pairs to a PostgreSQL database.
type Runner struct {
	Pool       *pgxpool.Pool
	Migrations []Pair
}

// AppliedVersions returns the set of applied migration versions, creating the
// bookkeeping table when missing.
func (r *Runner) AppliedVersions(ctx context.Context) (map[int]bool, error) {
	if _, err := r.Pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return nil, fmt.Errorf("schema_migrations hazırlanamadı: %w", err)
	}
	rows, err := r.Pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("uygulanmış sürümler okunamadı: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[int(v)] = true
	}
	return applied, rows.Err()
}

// Status returns all known migrations with their applied flag.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	applied, err := r.AppliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(r.Migrations))
	for _, m := range r.Migrations {
		out = append(out, Status{Version: m.Version, Name: m.Name, Applied: applied[m.Version]})
	}
	return out, nil
}

// Up applies every pending migration in order, each in its own transaction.
func (r *Runner) Up(ctx context.Context) (appliedCount int, err error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return 0, err
	}
	defer unlock(ctx)

	appliedSet, err := r.AppliedVersions(ctx)
	if err != nil {
		return 0, err
	}
	for _, m := range r.Migrations {
		if appliedSet[m.Version] {
			continue
		}
		if err := r.applyOne(ctx, m.Version, m.Name, m.UpSQL, true); err != nil {
			return appliedCount, fmt.Errorf("migration %04d_%s uygulanamadı: %w", m.Version, m.Name, err)
		}
		appliedCount++
	}
	return appliedCount, nil
}

// DownOne reverts the most recently applied migration.
func (r *Runner) DownOne(ctx context.Context) (reverted bool, err error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock(ctx)

	appliedSet, err := r.AppliedVersions(ctx)
	if err != nil {
		return false, err
	}
	byVersion := make(map[int]Pair, len(r.Migrations))
	for _, m := range r.Migrations {
		byVersion[m.Version] = m
	}
	latest := -1
	for v := range appliedSet {
		if _, known := byVersion[v]; !known {
			return false, fmt.Errorf("bilinmeyen uygulanan migration sürümü: %d (down devre dışı)", v)
		}
		if v > latest {
			latest = v
		}
	}
	if latest < 0 {
		return false, nil
	}
	m := byVersion[latest]
	if strings.TrimSpace(m.DownSQL) == "" {
		return false, fmt.Errorf("migration %d down betiği yok (geri alınamaz)", m.Version)
	}
	if err := r.applyOne(ctx, latest, m.Name, m.DownSQL, false); err != nil {
		return false, fmt.Errorf("migration %04d_%s geri alınamadı: %w", m.Version, m.Name, err)
	}
	return true, nil
}

// applyOne runs one migration script in a transaction and records it (up) or
// removes its record (down).
func (r *Runner) applyOne(ctx context.Context, version int, name, sql string, up bool) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if up {
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)
			 ON CONFLICT (version) DO UPDATE SET name = EXCLUDED.name, applied_at = now()`,
			version, name,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// lock acquires the migration advisory lock; the returned function releases it.
func (r *Runner) lock(ctx context.Context) (func(context.Context), error) {
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("advisory lock bağlantısı alınamadı: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("advisory lock alınamadı: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, fmt.Errorf("başka bir migration çalışıyor (advisory lock meşgul)")
	}
	release := func(uctx context.Context) {
		_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
		conn.Release()
	}
	return release, nil
}
