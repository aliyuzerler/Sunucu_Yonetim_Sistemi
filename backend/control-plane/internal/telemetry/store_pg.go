package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed telemetry store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const factsUpsert = `
INSERT INTO node_system_facts (node_id, os_id, os_version_id, os_pretty_name, os_home_url,
                               kernel_release, arch, systemd_version, package_manager,
                               systemd_available, source, collected_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (node_id) DO UPDATE SET
    os_id = EXCLUDED.os_id,
    os_version_id = EXCLUDED.os_version_id,
    os_pretty_name = EXCLUDED.os_pretty_name,
    os_home_url = EXCLUDED.os_home_url,
    kernel_release = EXCLUDED.kernel_release,
    arch = EXCLUDED.arch,
    systemd_version = EXCLUDED.systemd_version,
    package_manager = EXCLUDED.package_manager,
    systemd_available = EXCLUDED.systemd_available,
    source = EXCLUDED.source,
    collected_at = EXCLUDED.collected_at,
    updated_at = now()`

// UpsertFacts inserts or replaces the node's platform facts.
func (s *PGStore) UpsertFacts(ctx context.Context, f *SystemFacts) error {
	_, err := s.pool.Exec(ctx, factsUpsert,
		f.NodeID, f.OSID, f.OSVersionID, f.OSPrettyName, f.OSHomeURL,
		f.KernelRelease, f.Arch, f.SystemdVersion, f.PackageManager,
		f.SystemdAvailable, f.Source, f.CollectedAt)
	if err != nil {
		return fmt.Errorf("telemetry facts upsert: %w", err)
	}
	return nil
}

const factsSelect = `
SELECT node_id, os_id, os_version_id, os_pretty_name, os_home_url, kernel_release,
       arch, systemd_version, package_manager, systemd_available, source, collected_at
FROM node_system_facts WHERE node_id = $1`

// GetFacts returns the stored facts; ErrNotFound when absent.
func (s *PGStore) GetFacts(ctx context.Context, nodeID string) (*SystemFacts, error) {
	f := &SystemFacts{}
	err := s.pool.QueryRow(ctx, factsSelect, nodeID).Scan(
		&f.NodeID, &f.OSID, &f.OSVersionID, &f.OSPrettyName, &f.OSHomeURL,
		&f.KernelRelease, &f.Arch, &f.SystemdVersion, &f.PackageManager,
		&f.SystemdAvailable, &f.Source, &f.CollectedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry facts sorgusu: %w", err)
	}
	return f, nil
}

// InsertMetrics appends one snapshot and opportunistically prunes stale rows.
func (s *PGStore) InsertMetrics(ctx context.Context, nodeID string, snap *Snapshot) error {
	cpu, err := json.Marshal(snap.CPU)
	if err != nil {
		return err
	}
	mem, err := json.Marshal(snap.Memory)
	if err != nil {
		return err
	}
	disks, err := json.Marshal(snap.Disks)
	if err != nil {
		return err
	}
	nets, err := json.Marshal(snap.Networks)
	if err != nil {
		return err
	}
	if snap.Disks == nil {
		disks = []byte("[]")
	}
	if snap.Networks == nil {
		nets = []byte("[]")
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO node_metrics (node_id, collected_at, source, cpu, memory, disks, networks, uptime_seconds)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		nodeID, snap.CollectedAt, snap.Source, cpu, mem, disks, nets, snap.UptimeSeconds)
	if err != nil {
		return fmt.Errorf("telemetry metrics insert: %w", err)
	}
	return nil
}

const metricsSelect = `
SELECT collected_at, source, cpu, memory, disks, networks, uptime_seconds
FROM node_metrics WHERE node_id = $1`

// scanSnapshot converts one row into a Snapshot.
func scanSnapshot(row pgx.Row) (*Snapshot, error) {
	var snap Snapshot
	var cpu, mem, disks, nets []byte
	err := row.Scan(&snap.CollectedAt, &snap.Source, &cpu, &mem, &disks, &nets, &snap.UptimeSeconds)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry metrics sorgusu: %w", err)
	}
	_ = json.Unmarshal(cpu, &snap.CPU)
	_ = json.Unmarshal(mem, &snap.Memory)
	_ = json.Unmarshal(disks, &snap.Disks)
	_ = json.Unmarshal(nets, &snap.Networks)
	if snap.Disks == nil {
		snap.Disks = []DiskSample{}
	}
	if snap.Networks == nil {
		snap.Networks = []NetworkSample{}
	}
	return &snap, nil
}

// LatestMetrics returns the newest snapshot for a node.
func (s *PGStore) LatestMetrics(ctx context.Context, nodeID string) (*Snapshot, error) {
	return scanSnapshot(s.pool.QueryRow(ctx, metricsSelect+` ORDER BY collected_at DESC, id DESC LIMIT 1`, nodeID))
}

// ListMetrics returns up to limit snapshots, newest first.
func (s *PGStore) ListMetrics(ctx context.Context, nodeID string, limit int) ([]*Snapshot, error) {
	rows, err := s.pool.Query(ctx, metricsSelect+` ORDER BY collected_at DESC, id DESC LIMIT $2`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry metrics listesi: %w", err)
	}
	defer rows.Close()
	out := []*Snapshot{}
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// Prune deletes snapshots older than the cutoff.
func (s *PGStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM node_metrics WHERE collected_at < $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("telemetry prune: %w", err)
	}
	return tag.RowsAffected(), nil
}
