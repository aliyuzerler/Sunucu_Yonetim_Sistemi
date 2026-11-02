// evaluator.go — the Faz 13 alert evaluation loop. Runs on a ticker (like
// certmgmt.Watcher): reads enabled rules, resolves the node scope, pulls the
// latest telemetry snapshots and applies the dedupe/state machine. It never
// executes node operations — it is a pure control-plane observer.
package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NodeLister supplies the node IDs a global (all-nodes) rule covers.
type NodeLister interface {
	NodeIDs(ctx context.Context) ([]string, error)
}

// PGNodeLister reads node IDs directly from the inventory table.
type PGNodeLister struct {
	Pool *pgxpool.Pool
}

// NodeIDs lists all node UUIDs (bounded by the inventory size).
func (l PGNodeLister) NodeIDs(ctx context.Context) ([]string, error) {
	rows, err := l.Pool.Query(ctx, `SELECT id::text FROM nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("node listesi: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("node satırı: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Evaluator evaluates alert rules against node telemetry.
type Evaluator struct {
	Store     Store
	Telemetry *telemetry.Service
	Nodes     NodeLister
	Interval  time.Duration
	Now       func() time.Time
	Logger    *slog.Logger
}

// maxWindowSamples bounds the telemetry window query per rule evaluation.
const maxWindowSamples = 200

// Run drives the ticker loop until the context is cancelled.
func (e *Evaluator) Run(ctx context.Context) {
	interval := e.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	logger := e.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.EvaluateOnce(ctx, e.now()); err != nil {
				logger.Warn("alert_evaluation_failed", slog.String("error", err.Error()))
			}
		}
	}
}

func (e *Evaluator) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// EvaluateOnce runs one full evaluation pass (exported for tests).
func (e *Evaluator) EvaluateOnce(ctx context.Context, now time.Time) error {
	rules, err := e.Store.ListRules(ctx, false)
	if err != nil {
		return fmt.Errorf("kural okuma: %w", err)
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			// A corrupted rule must never crash the loop; skip honestly.
			return fmt.Errorf("kural %s doğrulaması: %w", r.ID, err)
		}
		if err := e.evaluateRule(ctx, r, now); err != nil {
			return fmt.Errorf("kural %s değerlendirmesi: %w", r.ID, err)
		}
	}
	return e.processSilenceExpiry(ctx, now)
}

// dimensionsFor lists the evaluated dimensions of one node for a rule.
// Disk rules without a pinned device evaluate EVERY disk of the newest
// snapshot (each device its own fingerprint → own alert).
func dimensionsFor(r Rule, snaps []*telemetry.Snapshot) []string {
	if r.Metric != MetricDisk {
		return []string{""}
	}
	if len(snaps) == 0 {
		return nil
	}
	if r.Dimension != "" {
		return []string{r.Dimension}
	}
	out := make([]string, 0, len(snaps[0].Disks))
	for _, d := range snaps[0].Disks {
		if d.TotalBytes > 0 {
			out = append(out, d.Device)
		}
	}
	return out
}

// sampleFor extracts the rule's value from one snapshot for one dimension.
func sampleFor(r Rule, dim string, snap *telemetry.Snapshot, now time.Time) (Sample, bool) {
	switch r.Metric {
	case MetricCPU:
		return Sample{Value: snap.CPU.Percent, CollectedAt: snap.CollectedAt}, true
	case MetricMemory:
		return Sample{Value: snap.Memory.UsedPercent, CollectedAt: snap.CollectedAt}, true
	case MetricDisk:
		for _, d := range snap.Disks {
			if dim != "" && d.Device != dim {
				continue
			}
			if d.TotalBytes <= 0 {
				continue
			}
			pct := float64(d.UsedBytes) / float64(d.TotalBytes) * 100
			return Sample{Value: pct, CollectedAt: snap.CollectedAt}, true
		}
		return Sample{}, false
	case MetricFreshness:
		return Sample{Value: now.Sub(snap.CollectedAt).Seconds(), CollectedAt: snap.CollectedAt}, true
	default:
		return Sample{}, false
	}
}

func (e *Evaluator) evaluateRule(ctx context.Context, r Rule, now time.Time) error {
	nodeIDs := []string{r.NodeID}
	if r.NodeID == "" {
		if e.Nodes == nil {
			return nil
		}
		ids, err := e.Nodes.NodeIDs(ctx)
		if err != nil {
			return err
		}
		nodeIDs = ids
	}

	for _, nodeID := range nodeIDs {
		if nodeID == "" {
			continue
		}
		snaps, ok, err := e.snapshots(ctx, r, nodeID)
		if err != nil {
			return err
		}
		if !ok {
			// No telemetry data for the node: honest skip (no alarm, no
			// auto-resolution — silence-expiry handles stale states).
			continue
		}
		for _, dim := range dimensionsFor(r, snaps) {
			samples := make([]Sample, 0, len(snaps))
			for _, snap := range snaps {
				if s, ok := sampleFor(r, dim, snap, now); ok {
					samples = append(samples, s)
				}
			}
			if len(samples) == 0 {
				continue
			}
			violated, value := r.Evaluate(samples, now)
			if violated {
				if _, err := e.Store.UpsertViolation(ctx, ViolationInput{
					Rule:      r,
					NodeID:    nodeID,
					Dimension: dim,
					Value:     value,
					Detail: fmt.Sprintf("değer %s, eşik %s (%s), pencere %ds",
						RenderValue(r.Metric, value), RenderValue(r.Metric, r.Threshold), string(r.Op), r.DurationSeconds),
				}); err != nil {
					return err
				}
				continue
			}
			// Not violating: auto-resolve any active alert of this fingerprint.
			if _, err := e.Store.ResolveOpenForRule(ctx, r.ID, nodeID, dim, "evaluator", now); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshots fetches telemetry snapshots for one node: the bounded history
// window for sustained rules, only the newest for instant rules.
func (e *Evaluator) snapshots(ctx context.Context, r Rule, nodeID string) ([]*telemetry.Snapshot, bool, error) {
	if e.Telemetry == nil {
		return nil, false, nil
	}
	if r.DurationSeconds > 0 {
		snaps, err := e.Telemetry.ListMetrics(ctx, nodeID, maxWindowSamples)
		if err != nil {
			if err == telemetry.ErrNotFound {
				return nil, false, nil
			}
			return nil, false, err
		}
		return snaps, len(snaps) > 0, nil
	}
	snap, err := e.Telemetry.LatestMetrics(ctx, nodeID)
	if err != nil {
		if err == telemetry.ErrNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	return []*telemetry.Snapshot{snap}, true, nil
}

// processSilenceExpiry reopens (violation persists) or resolves (recovered)
// every expired silence — OnSilenceExpiry state machine.
func (e *Evaluator) processSilenceExpiry(ctx context.Context, now time.Time) error {
	expired, err := e.Store.ListExpiredSilences(ctx, now)
	if err != nil {
		return err
	}
	if len(expired) == 0 {
		return nil
	}
	rules, err := e.Store.ListRules(ctx, true)
	if err != nil {
		return err
	}
	byID := map[string]Rule{}
	for _, r := range rules {
		byID[r.ID] = r
	}
	for _, a := range expired {
		r, ok := byID[a.RuleID]
		if !ok {
			// Rule deleted: resolve the orphaned alert.
			if _, err := e.Store.Transition(ctx, a.ID, StatusResolved, "evaluator", nil); err != nil && err != ErrInvalidState {
				return err
			}
			continue
		}
		still := e.stillViolating(ctx, r, a, now)
		to := StatusResolved
		if still {
			to = StatusOpen
		}
		if _, err := e.Store.Transition(ctx, a.ID, to, "evaluator", nil); err != nil && err != ErrInvalidState {
			return err
		}
	}
	return nil
}

// stillViolating re-evaluates the current value for one silenced alert.
func (e *Evaluator) stillViolating(ctx context.Context, r Rule, a Alert, now time.Time) bool {
	nodeID := a.NodeID
	if nodeID == "" {
		return false
	}
	snaps, ok, err := e.snapshots(ctx, r, nodeID)
	if err != nil || !ok || len(snaps) == 0 {
		return false
	}
	for _, dim := range dimensionsFor(r, snaps) {
		if dim != a.Dimension {
			continue
		}
		samples := make([]Sample, 0, len(snaps))
		for _, snap := range snaps {
			if s, ok := sampleFor(r, dim, snap, now); ok {
				samples = append(samples, s)
			}
		}
		if len(samples) == 0 {
			return false
		}
		v, _ := r.Evaluate(samples, now)
		return v
	}
	return false
}
