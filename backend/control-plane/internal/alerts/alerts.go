// Package alerts implements the Faz 13 alert lifecycle: rule evaluation,
// fingerprint DEDUPLICATION, and the open → acknowledged → silenced →
// resolved state machine. The state transitions are PURE functions here so
// they can be exhaustively unit-tested; the PG store persists the outcome.
package alerts

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Status is the alert lifecycle state (closed enum).
type Status string

const (
	StatusOpen         Status = "open"
	StatusAcknowledged Status = "acknowledged"
	StatusSilenced     Status = "silenced"
	StatusResolved     Status = "resolved"
)

// Severity is the alert importance (closed enum).
type Severity string

const (
	SevInfo     Severity = "info"
	SevWarning  Severity = "warning"
	SevCritical Severity = "critical"
)

// Metric identifies what a rule watches (closed enum).
type Metric string

const (
	MetricCPU       Metric = "cpu"
	MetricMemory    Metric = "memory"
	MetricDisk      Metric = "disk"
	MetricFreshness Metric = "metrics_freshness"
)

// Op is the comparison (closed enum).
type Op string

const (
	OpGT Op = "gt"
	OpLT Op = "lt"
)

// Domain errors (stable; handler layer maps to HTTP codes).
var (
	ErrNotFound       = errors.New("alerts: kayıt bulunamadı")
	ErrInvalidState   = errors.New("alerts: durum geçişi geçersiz")
	ErrInvalidRule    = errors.New("alerts: geçersiz kural")
	ErrCardinality    = errors.New("alerts: kural sınırı aşıldı")
	ErrSilenceExpired = errors.New("alerts: sessizleştirme süresi geçmiş")
)

// maxRules bounds the rule catalog (defense against unbounded growth;
// consistent with the bounded-cardinality posture of the whole phase).
const maxRules = 256

// Rule is one alert rule. Scope: NodeID empty = ALL nodes (global rule);
// otherwise the single node it belongs to. Dimension applies only to
// MetricDisk (the device name; empty = any/worst disk).
type Rule struct {
	ID              string
	Name            string
	NodeID          string // "" = all nodes
	Metric          Metric
	Dimension       string // disk device for MetricDisk, else ""
	Op              Op
	Threshold       float64
	DurationSeconds int // 0 = instant; >0 = window in which ALL samples must violate
	Severity        Severity
	Enabled         bool
	CreatedAt       time.Time
}

// maxNameLen bounds rule names; name must be printable (no control
// characters) — Turkish letters are welcome, control bytes are not.
const maxNameLen = 80

// validName allows any printable characters (unicode.IsPrint) up to 80
// bytes, rejecting control characters and empty/oversized names.
func validName(s string) bool {
	if s == "" || len(s) > maxNameLen {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// Validate checks a rule before create/update.
func (r Rule) Validate() error {
	if !validName(r.Name) {
		return fmt.Errorf("%w: kural adı 1..80 yazdırılabilir karakter olmalı (kontrol karakteri yasak)", ErrInvalidRule)
	}
	switch r.Metric {
	case MetricCPU, MetricMemory:
		if r.Dimension != "" {
			return fmt.Errorf("%w: cpu/memory kuralları dimension almaz", ErrInvalidRule)
		}
	case MetricDisk:
		if r.Dimension != "" && !deviceRE.MatchString(r.Dimension) {
			return fmt.Errorf("%w: disk device adı geçersiz", ErrInvalidRule)
		}
	case MetricFreshness:
		if r.Dimension != "" {
			return fmt.Errorf("%w: freshness kuralları dimension almaz", ErrInvalidRule)
		}
		if r.Threshold < 60 {
			return fmt.Errorf("%w: freshness eşiği ≥ 60 saniye olmalı", ErrInvalidRule)
		}
	default:
		return fmt.Errorf("%w: bilinmeyen metrik %q", ErrInvalidRule, r.Metric)
	}
	switch r.Op {
	case OpGT, OpLT:
	default:
		return fmt.Errorf("%w: op gt|lt olmalı", ErrInvalidRule)
	}
	if r.Threshold <= 0 || r.Threshold > 100000 {
		return fmt.Errorf("%w: eşik 0..100000 arasında olmalı", ErrInvalidRule)
	}
	if r.DurationSeconds < 0 || r.DurationSeconds > 3600 {
		return fmt.Errorf("%w: süre 0..3600 saniye olmalı", ErrInvalidRule)
	}
	switch r.Severity {
	case SevInfo, SevWarning, SevCritical:
	default:
		return fmt.Errorf("%w: severity info|warning|critical olmalı", ErrInvalidRule)
	}
	if r.NodeID != "" && !uuidRE.MatchString(r.NodeID) {
		return fmt.Errorf("%w: node_id UUID olmalı", ErrInvalidRule)
	}
	return nil
}

// deviceRE bounds disk device names (telemetry already bounds them ≤256).
var deviceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,63}$`)

// uuidRE matches canonical lowercase UUIDs (node IDs are server-issued).
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Alert is one lifecycle-tracked alert instance. Fingerprint is the DEDUPE
// key: rule + node + dimension. At most ONE non-resolved alert may exist
// per fingerprint (partial unique index in PG enforces it race-free).
type Alert struct {
	ID          string
	RuleID      string
	NodeID      string
	Dimension   string
	Fingerprint string
	Status      Status
	Severity    Severity
	Value       float64
	Occurrences int
	Title       string
	Detail      string

	FirstSeenAt time.Time
	LastSeenAt  time.Time

	AcknowledgedAt *time.Time
	AcknowledgedBy *string

	SilencedUntil *time.Time
	SilencedBy    *string

	ResolvedAt *time.Time
	ResolvedBy *string // "evaluator" for auto-resolution, user actor otherwise
}

// Fingerprint builds the dedupe key. Bounded: rule ID (uuid) + node UUID +
// dimension (allowlisted ≤64).
func Fingerprint(ruleID, nodeID, dimension string) string {
	return ruleID + "|" + nodeID + "|" + dimension
}

// RuleTitle renders the deterministic alert title.
func RuleTitle(r Rule) string {
	scope := "tüm düğümler"
	if r.NodeID != "" {
		scope = "düğüm"
	}
	if r.Metric == MetricDisk && r.Dimension != "" {
		return fmt.Sprintf("%s (%s %s %s)", r.Name, scope, r.Dimension, r.Metric)
	}
	return fmt.Sprintf("%s (%s %s)", r.Name, scope, r.Metric)
}

// violated applies the threshold comparison.
func (r Rule) violated(value float64) bool {
	if r.Op == OpGT {
		return value > r.Threshold
	}
	return value < r.Threshold
}

// Sample is one metric reading fed to evaluation.
type Sample struct {
	Value       float64
	CollectedAt time.Time
}

// Evaluate reports whether a rule is violated for one node given its
// samples (newest first, bounded list). DurationSeconds > 0 requires the
// whole window (now-duration .. now) to contain ONLY violating samples AND
// enough history: the oldest examined sample must reach back to (or before)
// the window start — otherwise there is INSUFFICIENT DATA and the rule does
// NOT fire (honest under-observation beats a false alarm). This is the
// honest "sustained violation" reading of the classic `for:` semantics.
func (r Rule) Evaluate(samples []Sample, now time.Time) (bool, float64) {
	if len(samples) == 0 {
		return false, 0
	}
	if r.DurationSeconds <= 0 {
		return r.violated(samples[0].Value), samples[0].Value
	}
	windowStart := now.Add(-time.Duration(r.DurationSeconds) * time.Second)
	count := 0
	worst := samples[0].Value
	covered := false
	for _, s := range samples {
		if s.CollectedAt.Before(windowStart) {
			// History extends beyond the window: the window is fully
			// observed (samples are newest-first).
			covered = true
			break
		}
		count++
		if !r.violated(s.Value) {
			return false, s.Value
		}
		if r.Op == OpGT && s.Value > worst {
			worst = s.Value
		}
		if r.Op == OpLT && s.Value < worst {
			worst = s.Value
		}
	}
	if count == 0 || !covered {
		// No sample inside the window, or the window is not fully observed —
		// insufficient data for a sustained-violation claim.
		return false, samples[0].Value
	}
	return true, worst
}

// Acknowledge applies the ACK transition. Rules (documented in
// monitoring.md): open|acknowledged → acknowledged (idempotent second ack
// is a no-op, not an error); silenced → typed ErrInvalidState (must re-open
// or resolve first); resolved → typed ErrInvalidState.
func (a *Alert) Acknowledge(actor string, now time.Time) error {
	switch a.Status {
	case StatusOpen, StatusAcknowledged:
		a.Status = StatusAcknowledged
		if a.AcknowledgedAt == nil {
			t := now
			actorCopy := actor
			a.AcknowledgedAt = &t
			a.AcknowledgedBy = &actorCopy
		}
		return nil
	case StatusSilenced:
		return fmt.Errorf("%w: sessizleştirilmiş alarm doğrudan onaylanamaz", ErrInvalidState)
	default:
		return fmt.Errorf("%w: çözülmüş alarm onaylanamaz", ErrInvalidState)
	}
}

// Silence applies the SILENCE transition for the given duration. Re-silence
// extends the window (idempotent extension). Resolved alerts cannot be
// silenced.
func (a *Alert) Silence(actor string, until time.Time, now time.Time) error {
	if a.Status == StatusResolved {
		return fmt.Errorf("%w: çözülmüş alarm sessizleştirilemez", ErrInvalidState)
	}
	actorCopy := actor
	a.Status = StatusSilenced
	a.SilencedUntil = &until
	a.SilencedBy = &actorCopy
	_ = now
	return nil
}

// Resolve applies the RESOLVE transition (manual). Any non-resolved state
// may resolve; resolving is idempotent-safe (no-op on resolved).
func (a *Alert) Resolve(actor string, now time.Time) error {
	if a.Status == StatusResolved {
		return nil
	}
	actorCopy := actor
	a.Status = StatusResolved
	t := now
	a.ResolvedAt = &t
	a.ResolvedBy = &actorCopy
	return nil
}

// OnSilenceExpiry decides the transition when silenced_until has passed:
// if the violation persists (stillViolating), the alert REOPENS (status
// open); otherwise it resolves (auto, by "evaluator"). This keeps silenced
// alerts from silently disappearing while honoring the operator's intent.
func OnSilenceExpiry(a *Alert, stillViolating bool, now time.Time) {
	if a.Status != StatusSilenced || a.SilencedUntil == nil || now.Before(*a.SilencedUntil) {
		return
	}
	if stillViolating {
		a.Status = StatusOpen
		return
	}
	a.Resolve("evaluator", now)
}

// RenderValue formats a metric value for display (bounded, no user data).
func RenderValue(m Metric, v float64) string {
	switch m {
	case MetricFreshness:
		return fmt.Sprintf("%ds", int(v))
	default:
		return fmt.Sprintf("%s%%", strings.TrimSpace(fmt.Sprintf("%.1f", v)))
	}
}
