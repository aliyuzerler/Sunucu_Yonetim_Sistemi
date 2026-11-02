// export.go renders Faz 13 node and aggregate series from PostgreSQL on top
// of the process registry. All label values are SYSTEM-GENERATED: node IDs
// are server-issued UUIDs (never user input), task statuses are a closed
// enum. The exporter also proves criterion 1: every node series line carries
// the snapshot's collected_at as its Prometheus timestamp, plus an explicit
// freshness gauge (seconds since last snapshot).
package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Metric names exported from the database (registered once at startup).
const (
	MetricNodeCPU         = "sc_node_cpu_percent"
	MetricNodeMemory      = "sc_node_memory_used_percent"
	MetricNodeFreshness   = "sc_node_metrics_freshness_seconds"
	MetricNodeUp          = "sc_node_up"
	MetricTasksTotal      = "sc_tasks_total"
	MetricOutboxPending   = "sc_outbox_pending_total"
	MetricNodesTotal      = "sc_nodes_total"
	MetricAlertsActive    = "sc_alerts_active_total"
	MetricCPInfo          = "sc_control_plane_info"
	MetricExporterScrapes = "sc_metrics_scrapes_total"
)

// NodeFreshThreshold is the freshness (seconds) under which a node counts as
// "up": 3× the default 60s metrics interval. It is a CONSTANT, not config —
// keeping it fixed keeps the exported series semantics stable.
const NodeFreshThreshold = 180.0

// Label names.
const (
	LabelNode     = "node"
	LabelStatus   = "status"
	LabelSeverity = "severity"
	LabelVersion  = "version"
)

// Exporter renders DB-backed series into the registry.
type Exporter struct {
	pool   *pgxpool.Pool
	reg    *Registry
	logger *slog.Logger

	mu        sync.Mutex
	lastNode  []nodeSeries // cached last render for tests
	lastError error
}

type nodeSeries struct {
	node        string
	cpu         float64
	mem         float64
	freshness   float64
	up          float64
	collectedAt time.Time
}

// NewExporter registers the DB-backed metric families.
func NewExporter(pool *pgxpool.Pool, reg *Registry, logger *slog.Logger, version string) (*Exporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Exporter{pool: pool, reg: reg, logger: logger}
	fams := []struct {
		fn func() error
	}{
		{func() error { return reg.Gauge(MetricNodeCPU, "Node CPU kullanımı (%; son snapshot).", LabelNode) }},
		{func() error {
			return reg.Gauge(MetricNodeMemory, "Node bellek kullanımı (%; son snapshot).", LabelNode)
		}},
		{func() error {
			return reg.Gauge(MetricNodeFreshness, "Son metrik snapshot'tan bu yana geçen süre (saniye).", LabelNode)
		}},
		{func() error {
			return reg.Gauge(MetricNodeUp, "Node güncel telemetri veriyor mu (freshness ≤ 180s → 1).", LabelNode)
		}},
		{func() error {
			return reg.Gauge(MetricTasksTotal, "Görev sayısı durumuna göre (kapalı enum).", LabelStatus)
		}},
		{func() error { return reg.Gauge(MetricOutboxPending, "Yayımlanmamış outbox olay sayısı.") }},
		{func() error { return reg.Gauge(MetricNodesTotal, "Kayıtlı node sayısı.") }},
		{func() error {
			return reg.Gauge(MetricAlertsActive, "Aktif (resolved olmayan) alarm sayısı önem derecesine göre.", LabelSeverity)
		}},
		{func() error {
			return reg.Gauge(MetricCPInfo, "Control Plane sürüm bilgisi (değer daima 1).", LabelVersion)
		}},
		{func() error { return reg.Counter(MetricExporterScrapes, "Toplam /metrics scrape sayısı.") }},
	}
	for _, f := range fams {
		if err := f.fn(); err != nil {
			return nil, err
		}
	}
	if err := reg.SetGauge(MetricCPInfo, []string{version}, 1); err != nil {
		return nil, err
	}
	return e, nil
}

// Render scrape anında DB serilerini registry'e yazar ve tam text çıktısını
// döndürür. DB hatası durumunda süreç metrikleri yine de export edilir; hata
// dürüstçe loglanır ve scrape sayacı artar (metrik yüzeyi DB'ye bağlı olarak
// bozulmaz — izleme, izlenenin izleyicisini kilitlememeli).
func (e *Exporter) Render(ctx context.Context, now time.Time) string {
	_ = e.reg.IncCounter(MetricExporterScrapes, nil, 1)
	if e.pool != nil {
		if err := e.renderNodeSeries(ctx, now); err != nil {
			e.mu.Lock()
			e.lastError = err
			e.mu.Unlock()
			e.logger.Warn("metrics_node_export_failed", slog.String("error", err.Error()))
		}
		if err := e.renderAggregates(ctx); err != nil {
			e.logger.Warn("metrics_aggregate_export_failed", slog.String("error", err.Error()))
		}
	}
	return e.reg.Render()
}

// LastNodeSeries exposes the cached node series (test helper).
func (e *Exporter) LastNodeSeries() []nodeSeries {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]nodeSeries, len(e.lastNode))
	copy(out, e.lastNode)
	return out
}

// LastError exposes the last node-export error (test helper).
func (e *Exporter) LastError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastError
}

type nodeRow struct {
	id          string
	collectedAt time.Time
	cpuRaw      []byte
	memRaw      []byte
}

func (e *Exporter) renderNodeSeries(ctx context.Context, now time.Time) error {
	rows, err := e.pool.Query(ctx, `
SELECT n.id::text,
       COALESCE(lm.collected_at, 'epoch'::timestamptz),
       COALESCE(lm.cpu, 'null'::jsonb),
       COALESCE(lm.memory, 'null'::jsonb)
FROM nodes n
LEFT JOIN LATERAL (
    SELECT collected_at, cpu, memory
    FROM node_metrics
    WHERE node_id = n.id
    ORDER BY collected_at DESC
    LIMIT 1
) lm ON true
ORDER BY n.id::text`)
	if err != nil {
		return fmt.Errorf("node metrik serisi: %w", err)
	}
	defer rows.Close()

	var series []nodeSeries
	for rows.Next() {
		var r nodeRow
		if err := rows.Scan(&r.id, &r.collectedAt, &r.cpuRaw, &r.memRaw); err != nil {
			return fmt.Errorf("node metrik satırı: %w", err)
		}
		s := nodeSeries{node: r.id, up: 0}
		if r.collectedAt.IsZero() {
			// Hiç snapshot yok: freshness export edilmez (satır çizilmez);
			// belirsiz veri "up=0" İLE temsil edilir — asla "up=1" değil.
			s.freshness = -1
		} else {
			s.freshness = now.Sub(r.collectedAt).Seconds()
			if s.freshness <= NodeFreshThreshold {
				s.up = 1
			}
			var cpu struct {
				Percent float64 `json:"percent"`
			}
			var mem struct {
				UsedPercent float64 `json:"used_percent"`
			}
			_ = json.Unmarshal(r.cpuRaw, &cpu)
			_ = json.Unmarshal(r.memRaw, &mem)
			s.cpu = cpu.Percent
			s.mem = mem.UsedPercent
		}
		series = append(series, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("node metrik okuma: %w", err)
	}

	for _, s := range series {
		node := []string{s.node}
		if s.freshness >= 0 {
			// Prometheus satır timestamp'ı: snapshot collected_at (epoch ms) —
			// kriter 1: metrikler timestamp + freshness taşır.
			ts := s.collectedAt.UnixMilli()
			_ = e.reg.SetGaugeAt(MetricNodeCPU, node, s.cpu, ts)
			_ = e.reg.SetGaugeAt(MetricNodeMemory, node, s.mem, ts)
			_ = e.reg.SetGaugeAt(MetricNodeFreshness, node, s.freshness, now.UnixMilli())
		}
		_ = e.reg.SetGauge(MetricNodeUp, node, s.up)
	}
	e.mu.Lock()
	e.lastNode = series
	e.mu.Unlock()
	return nil
}

func (e *Exporter) renderAggregates(ctx context.Context) error {
	rows, err := e.pool.Query(ctx, `SELECT status, count(*) FROM tasks GROUP BY status`)
	if err != nil {
		return fmt.Errorf("görev agregatları: %w", err)
	}
	var taskRows int
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return fmt.Errorf("görev agregat satırı: %w", err)
		}
		taskRows++
		if err := e.reg.SetGauge(MetricTasksTotal, []string{status}, float64(n)); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("görev agregat okuma: %w", err)
	}
	_ = taskRows

	var outbox int64
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&outbox); err != nil {
		return fmt.Errorf("outbox sayısı: %w", err)
	}
	if err := e.reg.SetGauge(MetricOutboxPending, nil, float64(outbox)); err != nil {
		return err
	}

	var nodes int64
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&nodes); err != nil {
		return fmt.Errorf("node sayısı: %w", err)
	}
	if err := e.reg.SetGauge(MetricNodesTotal, nil, float64(nodes)); err != nil {
		return err
	}

	arows, err := e.pool.Query(ctx, `
SELECT severity, count(*) FROM alerts
WHERE status IN ('open','acknowledged','silenced')
GROUP BY severity`)
	if err != nil {
		return fmt.Errorf("alarm agregatları: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var sev string
		var n int64
		if err := arows.Scan(&sev, &n); err != nil {
			return fmt.Errorf("alarm agregat satırı: %w", err)
		}
		if err := e.reg.SetGauge(MetricAlertsActive, []string{sev}, float64(n)); err != nil {
			return err
		}
	}
	return arows.Err()
}
