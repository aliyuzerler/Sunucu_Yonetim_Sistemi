// store_pg.go — PostgreSQL persistence for alert rules and alert instances.
// The dedupe guarantee is structural: a PARTIAL UNIQUE INDEX on fingerprint
// (WHERE status <> 'resolved') plus INSERT .. ON CONFLICT DO UPDATE means a
// repeated violation under concurrency produces ONE active alert with a
// bumped occurrence counter, never a second row (race-free at the index).
package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract (implemented by PGStore; faked in tests).
type Store interface {
	CountRules(ctx context.Context) (int, error)
	CreateRule(ctx context.Context, r Rule) (Rule, error)
	ListRules(ctx context.Context, includeDisabled bool) ([]Rule, error)
	DeleteRule(ctx context.Context, id string) error
	SetRuleEnabled(ctx context.Context, id string, enabled bool) error

	UpsertViolation(ctx context.Context, in ViolationInput) (Alert, error)
	ListAlerts(ctx context.Context, f ListFilter) ([]Alert, error)
	GetAlert(ctx context.Context, id string) (Alert, error)
	Transition(ctx context.Context, id string, to Status, actor string, silencedUntil *time.Time) (Alert, error)
	ListExpiredSilences(ctx context.Context, now time.Time) ([]Alert, error)
	ResolveOpenForRule(ctx context.Context, ruleID, nodeID, dimension, actor string, now time.Time) (int64, error)
}

// ViolationInput carries one evaluated violation into the dedupe upsert.
type ViolationInput struct {
	Rule      Rule
	NodeID    string
	Dimension string
	Value     float64
	Detail    string
}

// ListFilter bounds the alert listing (closed enum status + UUID node).
type ListFilter struct {
	Status Status
	NodeID string
	Limit  int
}

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed alert store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// Ensure maxRules is referenced (documentation constant).
var _ = maxRules

func scanAlertRow(row pgx.Row) (Alert, error) {
	var a Alert
	var silencedUntil, ackAt, resAt *time.Time
	var ackBy, silBy, resBy *string
	err := row.Scan(
		&a.ID, &a.RuleID, &a.NodeID, &a.Dimension, &a.Fingerprint, &a.Status,
		&a.Severity, &a.Value, &a.Occurrences, &a.Title, &a.Detail,
		&a.FirstSeenAt, &a.LastSeenAt,
		&ackAt, &ackBy, &silencedUntil, &silBy, &resAt, &resBy,
	)
	if err != nil {
		return Alert{}, err
	}
	a.AcknowledgedAt, a.AcknowledgedBy = ackAt, ackBy
	a.SilencedUntil, a.SilencedBy = silencedUntil, silBy
	a.ResolvedAt, a.ResolvedBy = resAt, resBy
	return a, nil
}

const alertCols = `id, rule_id, COALESCE(node_id::text,''), dimension, fingerprint, status,
severity, value, occurrences, title, detail,
first_seen_at, last_seen_at,
acknowledged_at, acknowledged_by, silenced_until, silenced_by, resolved_at, resolved_by`

// CountRules returns the rule catalog size (bounded by maxRules on create).
func (s *PGStore) CountRules(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM alert_rules`).Scan(&n); err != nil {
		return 0, fmt.Errorf("kural sayısı: %w", err)
	}
	return n, nil
}

// CreateRule inserts one rule after the maxRules bound check.
func (s *PGStore) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	n, err := s.CountRules(ctx)
	if err != nil {
		return Rule{}, err
	}
	if n >= maxRules {
		return Rule{}, ErrCardinality
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO alert_rules (name, node_id, metric, dimension, op, threshold, duration_seconds, severity, enabled)
VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, name, COALESCE(node_id::text,''), metric, dimension, op, threshold, duration_seconds, severity, enabled, created_at`,
		r.Name, r.NodeID, string(r.Metric), r.Dimension, string(r.Op), r.Threshold, r.DurationSeconds, string(r.Severity), r.Enabled)
	var out Rule
	var metric, op, sev string
	if err := row.Scan(&out.ID, &out.Name, &out.NodeID, &metric, &out.Dimension, &op, &out.Threshold, &out.DurationSeconds, &sev, &out.Enabled, &out.CreatedAt); err != nil {
		return Rule{}, fmt.Errorf("kural oluşturma: %w", err)
	}
	out.Metric, out.Op, out.Severity = Metric(metric), Op(op), Severity(sev)
	return out, nil
}

// ListRules returns rules (optionally only enabled ones), newest first.
func (s *PGStore) ListRules(ctx context.Context, includeDisabled bool) ([]Rule, error) {
	q := `SELECT id, name, COALESCE(node_id::text,''), metric, dimension, op, threshold, duration_seconds, severity, enabled, created_at FROM alert_rules`
	if !includeDisabled {
		q += ` WHERE enabled`
	}
	q += ` ORDER BY created_at DESC, id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("kural listesi: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		var metric, op, sev string
		if err := rows.Scan(&r.ID, &r.Name, &r.NodeID, &metric, &r.Dimension, &op, &r.Threshold, &r.DurationSeconds, &sev, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("kural satırı: %w", err)
		}
		r.Metric, r.Op, r.Severity = Metric(metric), Op(op), Severity(sev)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRule removes one rule (its alerts cascade).
func (s *PGStore) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("kural silme: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRuleEnabled toggles evaluation of a rule.
func (s *PGStore) SetRuleEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE alert_rules SET enabled = $2, updated_at = now() WHERE id = $1`, id, enabled)
	if err != nil {
		return fmt.Errorf("kural güncelleme: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertViolation is the DEDUPE core: one active alert per fingerprint.
// A resolved (or never-seen) fingerprint inserts a fresh alert; an active
// one gets occurrences+1, value and last_seen refreshed — same row.
func (s *PGStore) UpsertViolation(ctx context.Context, in ViolationInput) (Alert, error) {
	fp := Fingerprint(in.Rule.ID, in.NodeID, in.Dimension)
	title := RuleTitle(in.Rule)
	row := s.pool.QueryRow(ctx, `
INSERT INTO alerts (rule_id, node_id, dimension, fingerprint, status, severity, value, occurrences, title, detail, first_seen_at, last_seen_at)
VALUES ($1, NULLIF($2,'')::uuid, $3, $4, 'open', $5, $6, 1, $7, $8, now(), now())
ON CONFLICT (fingerprint) WHERE status <> 'resolved'
DO UPDATE SET value = EXCLUDED.value,
              occurrences = alerts.occurrences + 1,
              severity = EXCLUDED.severity,
              detail = EXCLUDED.detail,
              last_seen_at = now()
RETURNING `+alertCols,
		in.Rule.ID, in.NodeID, in.Dimension, fp, string(in.Rule.Severity), in.Value, title, in.Detail)
	a, err := scanAlertRow(row)
	if err != nil {
		return Alert{}, fmt.Errorf("alarm upsert: %w", err)
	}
	return a, nil
}

// ListAlerts returns alerts newest-first with bounded filters.
func (s *PGStore) ListAlerts(ctx context.Context, f ListFilter) ([]Alert, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	q := `SELECT ` + alertCols + ` FROM alerts WHERE true`
	args := []any{}
	if f.Status != "" {
		switch f.Status {
		case StatusOpen, StatusAcknowledged, StatusSilenced, StatusResolved:
		default:
			return nil, fmt.Errorf("%w: bilinmeyen durum %q", ErrInvalidRule, f.Status)
		}
		args = append(args, string(f.Status))
		q += fmt.Sprintf(` AND status = $%d`, len(args))
	}
	if f.NodeID != "" {
		if !uuidRE.MatchString(f.NodeID) {
			return nil, fmt.Errorf("%w: node_id UUID olmalı", ErrInvalidRule)
		}
		args = append(args, f.NodeID)
		q += fmt.Sprintf(` AND node_id = $%d::uuid`, len(args))
	}
	q += fmt.Sprintf(` ORDER BY last_seen_at DESC, id LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("alarm listesi: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlertRow(rows)
		if err != nil {
			return nil, fmt.Errorf("alarm satırı: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAlert fetches one alert by ID.
func (s *PGStore) GetAlert(ctx context.Context, id string) (Alert, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+alertCols+` FROM alerts WHERE id = $1`, id)
	a, err := scanAlertRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	if err != nil {
		return Alert{}, fmt.Errorf("alarm okuma: %w", err)
	}
	return a, nil
}

// Transition applies a lifecycle transition with its GUARD in SQL so two
// concurrent callers cannot both "win": ack/silence/resolve each carry their
// allowed source states in the UPDATE predicate; zero rows affected means
// either missing row or invalid transition (distinguished by a prior read).
func (s *PGStore) Transition(ctx context.Context, id string, to Status, actor string, silencedUntil *time.Time) (Alert, error) {
	if actor == "" {
		return Alert{}, fmt.Errorf("%w: aktör zorunlu", ErrInvalidRule)
	}
	var q string
	switch to {
	case StatusAcknowledged:
		q = `UPDATE alerts SET status='acknowledged', acknowledged_at=COALESCE(acknowledged_at, now()), acknowledged_by=COALESCE(acknowledged_by, $2) WHERE id=$1 AND status IN ('open','acknowledged') RETURNING ` + alertCols
	case StatusSilenced:
		if silencedUntil == nil {
			return Alert{}, fmt.Errorf("%w: sessizleştirme süresi zorunlu", ErrInvalidRule)
		}
		q = `UPDATE alerts SET status='silenced', silenced_until=$3, silenced_by=$2 WHERE id=$1 AND status <> 'resolved' RETURNING ` + alertCols
	case StatusResolved:
		q = `UPDATE alerts SET status='resolved', resolved_at=now(), resolved_by=$2 WHERE id=$1 AND status <> 'resolved' RETURNING ` + alertCols
	case StatusOpen:
		// Reopen after silence expiry: only a silenced alert whose window
		// has passed may reopen (evaluator-driven; SQL-guarded).
		q = `UPDATE alerts SET status='open' WHERE id=$1 AND status='silenced' AND silenced_until <= now() RETURNING ` + alertCols
	default:
		return Alert{}, fmt.Errorf("%w: hedef durum desteklenmiyor", ErrInvalidRule)
	}
	row := s.pool.QueryRow(ctx, q, id, actor, silencedUntil)
	a, err := scanAlertRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish not-found from invalid transition (honest errors).
		if _, gerr := s.GetAlert(ctx, id); gerr != nil {
			return Alert{}, ErrNotFound
		}
		return Alert{}, ErrInvalidState
	}
	if err != nil {
		return Alert{}, fmt.Errorf("alarm geçişi: %w", err)
	}
	return a, nil
}

// ListExpiredSilences returns silenced alerts whose window has passed.
func (s *PGStore) ListExpiredSilences(ctx context.Context, now time.Time) ([]Alert, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+alertCols+` FROM alerts WHERE status='silenced' AND silenced_until <= $1`, now)
	if err != nil {
		return nil, fmt.Errorf("süresi geçen sessizlikler: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlertRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sessizlik satırı: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ResolveOpenForRule auto-resolves every active alert of the exact
// rule+node+dimension fingerprint family (evaluator-driven recovery).
func (s *PGStore) ResolveOpenForRule(ctx context.Context, ruleID, nodeID, dimension, actor string, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE alerts SET status='resolved', resolved_at=$5, resolved_by=$4
WHERE rule_id=$1 AND node_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid AND dimension=$3 AND status <> 'resolved'`,
		ruleID, nodeID, dimension, actor, now)
	if err != nil {
		return 0, fmt.Errorf("otomatik çözüm: %w", err)
	}
	return tag.RowsAffected(), nil
}
