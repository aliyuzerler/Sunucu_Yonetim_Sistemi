// Package audit provides the append-only audit event recorder and query API.
// Every sensitive change must produce an event with actor, target, action,
// result and correlation ID (docs/phases/phase-02.md). Metadata is redacted
// before storage so secrets never reach the audit table or logs.
package audit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Result values for audit events.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultDenied  = "denied"
)

// Event is one immutable audit record.
type Event struct {
	Actor         string         // username or "system"
	ActorID       string         // optional user UUID
	Action        string         // namespaced verb, e.g. "node.create"
	TargetType    string         // e.g. "node", "user", "task"
	TargetID      string         // target identifier
	Result        string         // success | failure | denied
	CorrelationID string         // request/task correlation ID
	RequestID     string         // HTTP request ID, if any
	Metadata      map[string]any // must already be redacted; see Redact
}

// Validate checks structural invariants before an event is persisted.
func (e Event) Validate() error {
	if strings.TrimSpace(e.Actor) == "" || len(e.Actor) > 128 {
		return errors.New("audit: actor zorunlu (1..128)")
	}
	if !validAction(e.Action) {
		return errors.New("audit: action biçimi geçersiz (a-z0-9_. min 3)")
	}
	switch e.Result {
	case ResultSuccess, ResultFailure, ResultDenied:
	default:
		return fmt.Errorf("audit: geçersiz sonuç %q", e.Result)
	}
	if strings.TrimSpace(e.CorrelationID) == "" || len(e.CorrelationID) > 64 {
		return errors.New("audit: correlation ID zorunlu (1..64)")
	}
	return nil
}

func validAction(s string) bool {
	if len(s) < 3 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// Recorder persists audit events.
type Recorder interface {
	Record(ctx context.Context, e Event) error
}

// PGRecorder stores events in PostgreSQL. Append-only enforcement lives in
// the schema trigger; this type only ever INSERTs.
type PGRecorder struct {
	pool *pgxpool.Pool
}

// NewPGRecorder builds a PostgreSQL-backed recorder.
func NewPGRecorder(pool *pgxpool.Pool) *PGRecorder { return &PGRecorder{pool: pool} }

// Record inserts one event. Metadata is redacted HERE as defense in depth
// (callers must also redact), then validated and stored. Validation errors
// and SQL failures are returned.
func (r *PGRecorder) Record(ctx context.Context, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Metadata != nil {
		e.Metadata = Redact(e.Metadata).(map[string]any)
	}
	const q = `
INSERT INTO audit_events (actor, actor_id, action, target_type, target_id,
                          result, correlation_id, request_id, metadata)
VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9)`
	_, err := r.pool.Exec(ctx, q,
		e.Actor, e.ActorID, e.Action, e.TargetType, e.TargetID,
		e.Result, e.CorrelationID, e.RequestID, e.Metadata,
	)
	if err != nil {
		return fmt.Errorf("audit: kayıt yazılamadı: %w", err)
	}
	return nil
}

// Entry is one stored audit event as returned by queries.
type Entry struct {
	ID            int64          `json:"id"`
	Actor         string         `json:"actor"`
	ActorID       *string        `json:"actor_id,omitempty"`
	Action        string         `json:"action"`
	TargetType    string         `json:"target_type"`
	TargetID      string         `json:"target_id"`
	Result        string         `json:"result"`
	CorrelationID string         `json:"correlation_id"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// Page is one cursor-paginated result page.
type Page struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

// Filter restricts audit queries. Fields are allowlisted; empty means "all".
type Filter struct {
	Actor  string
	Action string
	Limit  int
	Cursor string // keyset cursor: last seen event ID
}

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// Query returns audit entries, newest first, using keyset pagination on the
// monotonically increasing identity column.
func Query(ctx context.Context, pool *pgxpool.Pool, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	where := []string{"true"}
	args := []any{}
	if f.Actor != "" {
		args = append(args, f.Actor)
		where = append(where, fmt.Sprintf("actor = $%d", len(args)))
	}
	if f.Action != "" {
		args = append(args, f.Action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return Page{}, fmt.Errorf("audit: geçersiz imleç: %w", err)
		}
		args = append(args, cur)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	args = append(args, limit+1)

	q := `SELECT id, actor, actor_id, action, target_type, target_id, result,
                 correlation_id, metadata, created_at
          FROM audit_events
          WHERE ` + strings.Join(where, " AND ") + `
          ORDER BY id DESC
          LIMIT $` + strconv.Itoa(len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return Page{}, fmt.Errorf("audit: sorgu başarısız: %w", err)
	}
	defer rows.Close()

	page := Page{Entries: []Entry{}}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Actor, &e.ActorID, &e.Action, &e.TargetType,
			&e.TargetID, &e.Result, &e.CorrelationID, &e.Metadata, &e.CreatedAt); err != nil {
			return Page{}, fmt.Errorf("audit: satır okunamadı: %w", err)
		}
		page.Entries = append(page.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("audit: sorgu hatası: %w", err)
	}
	if len(page.Entries) > limit {
		page.HasMore = true
		page.Entries = page.Entries[:limit]
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = encodeCursor(last.ID)
	}
	return page, nil
}

func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeCursor(s string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(raw), 10, 64)
}
