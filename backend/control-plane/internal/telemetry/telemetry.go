// Package telemetry persists agent-reported system facts and metric
// snapshots (Faz 04). Facts are upserted per node (the latest report wins);
// metrics are append-only time series with server-side retention pruning.
//
// Security invariants:
//   - Only agents over mTLS (node-bound envelope) write here.
//   - Ingestion is rate-limited upstream (agentsrv) to protect the database.
//   - Values are validated and bounded BEFORE persistence.
package telemetry

import (
	"context"
	"errors"
	"time"
)

// Retention bounds metric history (docs/phases/phase-04.md: basic telemetry,
// not a TSDB). Older snapshots are pruned opportunistically.
const Retention = 7 * 24 * time.Hour

// MinInterval is the fastest accepted cadence per node (agentsrv enforces it;
// the constant documents the contract).
const MinInterval = 10 * time.Second

// Limits bound one snapshot's collections.
const (
	MaxDisks    = 64
	MaxNetworks = 64
)

// ErrInvalidSnapshot rejects structurally invalid metric payloads.
var ErrInvalidSnapshot = errors.New("telemetry: geçersiz metrik anlık görüntüsü")

// ErrNotFound is returned when facts or metrics are missing for a node.
var ErrNotFound = errors.New("telemetry: kayıt bulunamadı")

// CPUSample is the JSON shape stored in node_metrics.cpu.
type CPUSample struct {
	Percent      float64 `json:"percent"`
	LogicalCores uint32  `json:"logical_cores"`
	Load1m       float64 `json:"load_1m"`
	Load5m       float64 `json:"load_5m"`
	Load15m      float64 `json:"load_15m"`
}

// MemorySample is the JSON shape stored in node_metrics.memory.
type MemorySample struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

// DiskSample is one entry of node_metrics.disks.
type DiskSample struct {
	Device         string `json:"device"`
	MountPoint     string `json:"mount_point"`
	FSType         string `json:"fs_type"`
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// NetworkSample is one entry of node_metrics.networks.
type NetworkSample struct {
	Interface string `json:"interface"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
}

// Snapshot is one metric sample ready for persistence. JSON tags define the
// REST wire shape.
type Snapshot struct {
	CollectedAt   time.Time       `json:"collected_at"`
	Source        string          `json:"source"`
	CPU           CPUSample       `json:"cpu"`
	Memory        MemorySample    `json:"memory"`
	Disks         []DiskSample    `json:"disks"`
	Networks      []NetworkSample `json:"networks"`
	UptimeSeconds float64         `json:"uptime_seconds"`
}

// SystemFacts is the agent-reported platform data.
type SystemFacts struct {
	NodeID           string    `json:"node_id"`
	OSID             string    `json:"os_id"`
	OSVersionID      string    `json:"os_version_id"`
	OSPrettyName     string    `json:"os_pretty_name"`
	OSHomeURL        string    `json:"os_home_url"`
	KernelRelease    string    `json:"kernel_release"`
	Arch             string    `json:"arch"`
	SystemdVersion   string    `json:"systemd_version"`
	PackageManager   string    `json:"package_manager"`
	SystemdAvailable bool      `json:"systemd_available"`
	DistroFamily     string    `json:"distro_family"`  // Faz 06
	DistroLike       string    `json:"distro_like"`    // Faz 06
	SupportState     string    `json:"support_state"`  // Faz 06: supported|untested|unknown
	SupportReason    string    `json:"support_reason"` // Faz 06
	Source           string    `json:"source"`
	CollectedAt      time.Time `json:"collected_at"`
}

// Validate bounds-checks a snapshot before persistence.
func (s *Snapshot) Validate() error {
	if s == nil || s.CollectedAt.IsZero() {
		return ErrInvalidSnapshot
	}
	if len(s.Source) > 128 || len(s.Disks) > MaxDisks || len(s.Networks) > MaxNetworks {
		return ErrInvalidSnapshot
	}
	for _, d := range s.Disks {
		if len(d.Device) > 256 || len(d.MountPoint) > 4096 || len(d.FSType) > 64 {
			return ErrInvalidSnapshot
		}
	}
	for _, n := range s.Networks {
		if len(n.Interface) > 128 {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

// Store is the persistence contract (PostgreSQL implementation in store_pg.go).
type Store interface {
	UpsertFacts(ctx context.Context, f *SystemFacts) error
	GetFacts(ctx context.Context, nodeID string) (*SystemFacts, error)
	InsertMetrics(ctx context.Context, nodeID string, s *Snapshot) error
	LatestMetrics(ctx context.Context, nodeID string) (*Snapshot, error)
	ListMetrics(ctx context.Context, nodeID string, limit int) ([]*Snapshot, error)
	Prune(ctx context.Context, olderThan time.Time) (int64, error)
}

// Service is the telemetry domain service.
type Service struct {
	store Store
}

// NewService builds the telemetry service.
func NewService(store Store) *Service { return &Service{store: store} }

// UpsertFacts stores the latest agent-reported facts (idempotent upsert).
func (svc *Service) UpsertFacts(ctx context.Context, f *SystemFacts) error {
	if f == nil || f.NodeID == "" {
		return ErrNotFound
	}
	bound := func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	}
	f.OSID = bound(f.OSID, 64)
	f.OSVersionID = bound(f.OSVersionID, 64)
	f.OSPrettyName = bound(f.OSPrettyName, 256)
	f.OSHomeURL = bound(f.OSHomeURL, 256)
	f.KernelRelease = bound(f.KernelRelease, 128)
	f.Arch = bound(f.Arch, 32)
	f.SystemdVersion = bound(f.SystemdVersion, 32)
	f.PackageManager = bound(f.PackageManager, 32)
	f.DistroFamily = bound(f.DistroFamily, 32)
	f.DistroLike = bound(f.DistroLike, 128)
	f.SupportState = bound(f.SupportState, 16)
	f.SupportReason = bound(f.SupportReason, 256)
	f.Source = bound(f.Source, 128)
	if f.CollectedAt.IsZero() {
		f.CollectedAt = time.Now().UTC()
	}
	return svc.store.UpsertFacts(ctx, f)
}

// GetFacts returns the stored facts for one node.
func (svc *Service) GetFacts(ctx context.Context, nodeID string) (*SystemFacts, error) {
	return svc.store.GetFacts(ctx, nodeID)
}

// InsertMetrics validates and stores one snapshot.
func (svc *Service) InsertMetrics(ctx context.Context, nodeID string, s *Snapshot) error {
	if nodeID == "" {
		return ErrNotFound
	}
	if err := s.Validate(); err != nil {
		return err
	}
	return svc.store.InsertMetrics(ctx, nodeID, s)
}

// LatestMetrics returns the newest snapshot for a node.
func (svc *Service) LatestMetrics(ctx context.Context, nodeID string) (*Snapshot, error) {
	return svc.store.LatestMetrics(ctx, nodeID)
}

// ListMetrics returns up to limit snapshots (newest first), limit ≤ 200.
func (svc *Service) ListMetrics(ctx context.Context, nodeID string, limit int) ([]*Snapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return svc.store.ListMetrics(ctx, nodeID, limit)
}

// Prune deletes snapshots older than the retention window.
func (svc *Service) Prune(ctx context.Context) (int64, error) {
	return svc.store.Prune(ctx, time.Now().UTC().Add(-Retention))
}
