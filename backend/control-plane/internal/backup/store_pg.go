package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists backup and restore-drill records.
type Store interface {
	InsertBackup(ctx context.Context, rec *Record) (string, error)
	// CompleteBackup records the terminal state + verification outcome
	// (kriter 2: no success without a recorded verify pass).
	CompleteBackup(ctx context.Context, id, status string, size int64, sha, snapshotID, storagePath, detail string) (Record, error)
	UpdateVerify(ctx context.Context, id, state, detail string) (Record, error)
	GetBackup(ctx context.Context, id string) (Record, error)
	ListBackups(ctx context.Context, scope, nodeID string, limit int) ([]Record, error)

	InsertDrill(ctx context.Context, backupID, nodeID, target, actor, taskID string) (Drill, error)
	CompleteDrill(ctx context.Context, id, status, detail string, tables int, rows int64) (Drill, error)
	ListDrills(ctx context.Context, nodeID string, limit int) ([]Drill, error)
}

// PGStore implements Store on PostgreSQL (migration 0018).
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore builds the SQL-backed backup store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const recCols = `id, scope, COALESCE(node_id::text, ''), kind, status, verify_state,
        COALESCE(verify_at::text, ''), verify_detail, size_bytes, sha256, snapshot_id,
        storage_path, COALESCE(task_id::text, ''), detail, created_at::text, updated_at::text`

func scanRec(row pgx.Row) (Record, error) {
	var r Record
	err := row.Scan(&r.ID, &r.Scope, &r.NodeID, &r.Kind, &r.Status, &r.VerifyState,
		&r.VerifyAt, &r.VerifyDetail, &r.SizeBytes, &r.SHA256, &r.SnapshotID,
		&r.StoragePath, &r.TaskID, &r.Detail, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrBackupNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("backup store: okuma: %w", err)
	}
	return r, nil
}

// InsertBackup creates the running-state record.
func (s *PGStore) InsertBackup(ctx context.Context, rec *Record) (string, error) {
	var id string
	var nodeID any
	if rec.NodeID != "" {
		nodeID = rec.NodeID
	}
	err := s.pool.QueryRow(ctx, `
INSERT INTO backups (scope, node_id, kind, status, task_id)
VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid)
RETURNING id`,
		rec.Scope, nodeID, rec.Kind, rec.Status, rec.TaskID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("backup store: ekleme: %w", err)
	}
	return id, nil
}

// CompleteBackup records the terminal state + verification outcome.
func (s *PGStore) CompleteBackup(ctx context.Context, id, status string, size int64, sha, snapshotID, storagePath, detail string) (Record, error) {
	vs := VerifyUnverified
	if status == StatusSuccess {
		vs = VerifyVerified
	}
	row := s.pool.QueryRow(ctx, `
UPDATE backups SET status = $2, size_bytes = $3, sha256 = $4, snapshot_id = $5,
       storage_path = $6, detail = $7, verify_state = $8,
       verify_at = CASE WHEN $8 = 'verified' THEN now() ELSE NULL END,
       updated_at = now()
WHERE id = $1
RETURNING `+recCols, id, status, size, sha, snapshotID, storagePath, detail, vs)
	return scanRec(row)
}

// UpdateVerify records a (re-)verification outcome.
func (s *PGStore) UpdateVerify(ctx context.Context, id, state, detail string) (Record, error) {
	row := s.pool.QueryRow(ctx, `
UPDATE backups SET verify_state = $2, verify_at = CASE WHEN $2 = 'verified' THEN now() ELSE verify_at END,
       verify_detail = $3, updated_at = now()
WHERE id = $1
RETURNING `+recCols, id, state, detail)
	return scanRec(row)
}

// GetBackup returns one record.
func (s *PGStore) GetBackup(ctx context.Context, id string) (Record, error) {
	return scanRec(s.pool.QueryRow(ctx, `SELECT `+recCols+` FROM backups WHERE id = $1`, id))
}

// ListBackups returns recent records, optionally filtered.
func (s *PGStore) ListBackups(ctx context.Context, scope, nodeID string, limit int) ([]Record, error) {
	q := `SELECT ` + recCols + ` FROM backups WHERE TRUE`
	args := []any{}
	if scope != "" {
		args = append(args, scope)
		q += fmt.Sprintf(" AND scope = $%d", len(args))
	}
	if nodeID != "" {
		if _, err := pgxParseUUID(nodeID); err != nil {
			return nil, ErrInvalidNodeScope
		}
		args = append(args, nodeID)
		q += fmt.Sprintf(" AND node_id = $%d::uuid", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("backup store: liste: %w", err)
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRec(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertDrill creates the running-state drill record.
func (s *PGStore) InsertDrill(ctx context.Context, backupID, nodeID, target, actor, taskID string) (Drill, error) {
	var id string
	var nodeArg, taskArg any
	if nodeID != "" {
		nodeArg = nodeID
	}
	if taskID != "" {
		taskArg = taskID
	}
	err := s.pool.QueryRow(ctx, `
INSERT INTO restore_drills (backup_id, node_id, target, status, actor, task_id)
VALUES ($1, $2, $3, 'running', NULLIF($4, '')::text, NULLIF($5, '')::uuid)
RETURNING id`, backupID, nodeArg, target, actor, taskArg).Scan(&id)
	if err != nil {
		return Drill{}, fmt.Errorf("backup store: tatbikat ekleme: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return Drill{ID: id, BackupID: backupID, NodeID: nodeID, Target: target,
		Status: StatusRunning, Actor: actor, TaskID: taskID, CreatedAt: now}, nil
}

// CompleteDrill records the drill outcome.
func (s *PGStore) CompleteDrill(ctx context.Context, id, status, detail string, tables int, rows int64) (Drill, error) {
	row := s.pool.QueryRow(ctx, `
UPDATE restore_drills SET status = $2, detail = $3, tables_restored = $4, rows_restored = $5
WHERE id = $1
RETURNING id, COALESCE(backup_id::text, ''), COALESCE(node_id::text, ''), target, status,
       detail, tables_restored, rows_restored, COALESCE(task_id::text, ''),
       COALESCE(actor, ''), created_at::text`, id, status, detail, tables, rows)
	var d Drill
	err := row.Scan(&d.ID, &d.BackupID, &d.NodeID, &d.Target, &d.Status,
		&d.Detail, &d.TablesRestored, &d.RowsRestored, &d.TaskID, &d.Actor, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Drill{}, ErrBackupNotFound
	}
	if err != nil {
		return Drill{}, fmt.Errorf("backup store: tatbikat güncelleme: %w", err)
	}
	return d, nil
}

// ListDrills returns recent drills, optionally node-filtered.
func (s *PGStore) ListDrills(ctx context.Context, nodeID string, limit int) ([]Drill, error) {
	q := `SELECT id, backup_id::text, COALESCE(node_id::text, ''), target, status, detail,
       tables_restored, rows_restored, COALESCE(task_id::text, ''), COALESCE(actor, ''),
       created_at::text FROM restore_drills WHERE TRUE`
	args := []any{}
	if nodeID != "" {
		if _, err := pgxParseUUID(nodeID); err != nil {
			return nil, ErrInvalidNodeScope
		}
		args = append(args, nodeID)
		q += fmt.Sprintf(" AND node_id = $%d::uuid", len(args))
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("backup store: tatbikat listesi: %w", err)
	}
	defer rows.Close()
	out := []Drill{}
	for rows.Next() {
		var d Drill
		if err := rows.Scan(&d.ID, &d.BackupID, &d.NodeID, &d.Target, &d.Status,
			&d.Detail, &d.TablesRestored, &d.RowsRestored, &d.TaskID, &d.Actor, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ErrInvalidNodeScope marks a malformed node filter.
var ErrInvalidNodeScope = errors.New("backup: node kimliği geçersiz")

// pgxParseUUID validates UUID shape (defense-in-depth for query filters).
func pgxParseUUID(s string) (string, error) {
	if len(s) != 36 {
		return "", errors.New("uuid uzunluğu")
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return "", errors.New("uuid ayraç")
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", errors.New("uuid karakter")
		}
	}
	return s, nil
}
