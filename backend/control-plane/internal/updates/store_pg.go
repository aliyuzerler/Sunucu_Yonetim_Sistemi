package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore builds the PostgreSQL-backed store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const artCols = `id, target, version, file_name, size_bytes, sha256, signature,
 min_prev, api_version, notes, state, manifest, published_by, published_at`

func scanArt(row pgx.Row) (Artifact, error) {
	var a Artifact
	var manifest json.RawMessage
	err := row.Scan(&a.ID, &a.Target, &a.Version, &a.FileName, &a.SizeBytes,
		&a.SHA256, &a.Signature, &a.MinPrev, &a.APIVersion, &a.Notes,
		&a.State, &manifest, &a.PublishedBy, &a.PublishedAt)
	if err != nil {
		return Artifact{}, err
	}
	a.Manifest = manifest
	return a, nil
}

// Insert stores one published artifact (uniqueness target+version enforced
// by the schema).
func (s *PGStore) Insert(ctx context.Context, a Artifact, manifest json.RawMessage) (Artifact, error) {
	row := s.pool.QueryRow(ctx, `
INSERT INTO update_artifacts (target, version, file_name, size_bytes, sha256,
                              signature, min_prev, api_version, notes, state,
                              manifest, published_by)
VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12)
RETURNING `+artCols,
		a.Target, a.Version, a.FileName, a.SizeBytes, a.SHA256, a.Signature,
		a.MinPrev, a.APIVersion, a.Notes, a.State, manifest, a.PublishedBy)
	art, err := scanArt(row)
	if err != nil {
		return Artifact{}, fmt.Errorf("update artifact yazılamadı: %w", err)
	}
	return art, nil
}

// Get loads one artifact by id.
func (s *PGStore) Get(ctx context.Context, id string) (Artifact, error) {
	if !uuidRE.MatchString(id) {
		return Artifact{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `SELECT `+artCols+` FROM update_artifacts WHERE id=$1`, id)
	a, err := scanArt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	return a, err
}

// GetByVersion loads by unique (target, version).
func (s *PGStore) GetByVersion(ctx context.Context, target, version string) (Artifact, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+artCols+` FROM update_artifacts WHERE target=$1 AND version=$2`, target, version)
	a, err := scanArt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}
	return a, err
}

// List returns artifacts ordered by target, version (target optional).
func (s *PGStore) List(ctx context.Context, target string) ([]Artifact, error) {
	q := `SELECT ` + artCols + ` FROM update_artifacts`
	args := []any{}
	if target != "" {
		q += ` WHERE target=$1`
		args = append(args, target)
	}
	q += ` ORDER BY target, version`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetState transitions published ↔ revoked.
func (s *PGStore) SetState(ctx context.Context, id, state string) error {
	if state != StatePublished && state != StateRevoked {
		return ErrBadManifest
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE update_artifacts SET state=$2 WHERE id=$1`, id, state)
	if err != nil {
		return fmt.Errorf("update artifact durumu: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
