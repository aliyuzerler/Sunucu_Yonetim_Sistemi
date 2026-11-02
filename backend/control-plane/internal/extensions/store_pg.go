package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL (ADR-0004).
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore builds the PostgreSQL-backed store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const extCols = `id, name, display_name, vendor, version, api_version, min_cp_version,
 artifact_sha256, state, manifest, delegated_ops, capabilities, registered_by,
 registered_at, updated_at`

func scanExt(row pgx.Row) (Extension, json.RawMessage, error) {
	var e Extension
	var manifest json.RawMessage
	var delegated, caps []byte
	err := row.Scan(&e.ID, &e.Name, &e.DisplayName, &e.Vendor, &e.Version,
		&e.APIVersion, &e.MinCPVersion, &e.ArtifactSHA, &e.State, &manifest,
		&delegated, &caps, &e.RegisteredBy, &e.RegisteredAt, &e.UpdatedAt)
	if err != nil {
		return Extension{}, nil, err
	}
	e.Manifest = manifest
	_ = json.Unmarshal(delegated, &e.DelegatedOps)
	_ = json.Unmarshal(caps, &e.Capabilities)
	return e, manifest, nil
}

// Upsert inserts or replaces by unique name. State transitions and grants are
// handled by the service; this only persists the record.
func (s *PGStore) Upsert(ctx context.Context, e Extension, manifest json.RawMessage) (Extension, error) {
	delegated, _ := json.Marshal(e.DelegatedOps)
	caps, _ := json.Marshal(e.Capabilities)
	const q = `
INSERT INTO extensions (name, display_name, vendor, version, api_version,
                        min_cp_version, artifact_sha256, state, manifest,
                        delegated_ops, capabilities, registered_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (name) DO UPDATE SET
  display_name=EXCLUDED.display_name, vendor=EXCLUDED.vendor,
  version=EXCLUDED.version, api_version=EXCLUDED.api_version,
  min_cp_version=EXCLUDED.min_cp_version, artifact_sha256=EXCLUDED.artifact_sha256,
  state='pending', manifest=EXCLUDED.manifest, delegated_ops=EXCLUDED.delegated_ops,
  capabilities=EXCLUDED.capabilities, registered_by=EXCLUDED.registered_by,
  updated_at=now()
RETURNING id, name, display_name, vendor, version, api_version, min_cp_version,
          artifact_sha256, state, manifest, delegated_ops, capabilities,
          registered_by, registered_at, updated_at`
	row := s.pool.QueryRow(ctx, q, e.Name, e.DisplayName, e.Vendor, e.Version,
		e.APIVersion, e.MinCPVersion, e.ArtifactSHA, e.State, manifest,
		delegated, caps, e.RegisteredBy)
	saved, _, err := scanExt(row)
	return saved, err
}

// Get loads one extension plus its active grants.
func (s *PGStore) Get(ctx context.Context, id string) (Extension, json.RawMessage, error) {
	if !uuidRE.MatchString(id) {
		return Extension{}, nil, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, `SELECT `+extCols+` FROM extensions WHERE id=$1`, id)
	e, manifest, err := scanExt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Extension{}, nil, ErrNotFound
	}
	if err != nil {
		return Extension{}, nil, err
	}
	e.Granted, err = s.grants(ctx, e.ID)
	return e, manifest, err
}

// GetByName loads by unique name.
func (s *PGStore) GetByName(ctx context.Context, name string) (Extension, json.RawMessage, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+extCols+` FROM extensions WHERE name=$1`, name)
	e, manifest, err := scanExt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Extension{}, nil, ErrNotFound
	}
	if err != nil {
		return Extension{}, nil, err
	}
	e.Granted, err = s.grants(ctx, e.ID)
	return e, manifest, err
}

// List returns all extensions ordered by name.
func (s *PGStore) List(ctx context.Context) ([]Extension, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+extCols+` FROM extensions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Extension
	for rows.Next() {
		e, _, err := scanExt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) grants(ctx context.Context, extID string) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `
SELECT action, risk, granted_by, granted_at, revoked_at
  FROM extension_grants WHERE extension_id=$1 ORDER BY granted_at`, extID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Action, &g.Risk, &g.GrantedBy, &g.GrantedAt, &g.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertGrant (re)activates one permission grant for the extension.
func (s *PGStore) UpsertGrant(ctx context.Context, extID string, g Grant) (Grant, error) {
	row := s.pool.QueryRow(ctx, `
INSERT INTO extension_grants (extension_id, action, risk, granted_by)
VALUES ($1,$2,$3,$4)
ON CONFLICT (extension_id, action) DO UPDATE SET
  revoked_at=NULL, risk=EXCLUDED.risk, granted_by=EXCLUDED.granted_by,
  granted_at=now()
RETURNING action, risk, granted_by, granted_at, revoked_at`,
		extID, g.Action, g.Risk, g.GrantedBy)
	var out Grant
	err := row.Scan(&out.Action, &out.Risk, &out.GrantedBy, &out.GrantedAt, &out.RevokedAt)
	if err != nil {
		return Grant{}, fmt.Errorf("extension grant yazılamadı: %w", err)
	}
	return out, nil
}

// RevokeGrant sets revoked_at for one action (idempotent when absent).
func (s *PGStore) RevokeGrant(ctx context.Context, extID, action string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE extension_grants SET revoked_at=now()
          WHERE extension_id=$1 AND action=$2 AND revoked_at IS NULL`, extID, action)
	if err != nil {
		return fmt.Errorf("extension grant geri alınamadı: %w", err)
	}
	_ = ct
	return nil
}

// RevokeAllGrants withdraws every active grant of the extension.
func (s *PGStore) RevokeAllGrants(ctx context.Context, extID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE extension_grants SET revoked_at=now()
          WHERE extension_id=$1 AND revoked_at IS NULL`, extID)
	if err != nil {
		return fmt.Errorf("extension grants geri alınamadı: %w", err)
	}
	return nil
}

// uuidRE mirrors the task-layer UUID shape check (same contract).
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
