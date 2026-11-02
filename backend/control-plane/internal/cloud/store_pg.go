package cloud

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists provider registrations (credentials sealed, never returned).
type Store interface {
	Create(ctx context.Context, name string, kind Kind, endpoint, region string, encrypted []byte, credKeys []string) (ProviderRow, error)
	List(ctx context.Context) ([]ProviderRow, error)
	Get(ctx context.Context, id string) (ProviderRow, error)
	Delete(ctx context.Context, id string) error
}

// ProviderRow is the persisted registration (no credential material).
type ProviderRow struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         Kind      `json:"kind"`
	Endpoint     string    `json:"endpoint"`
	Region       string    `json:"region"`
	CredKeyNames []string  `json:"credential_keys"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PGStore implements Store on PostgreSQL (migration 0018).
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore builds the SQL-backed cloud store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// MaxProviders bounds registered providers (UI + abuse guard).
const MaxProviders = 20

// Create inserts one provider with a sealed credential blob.
func (s *PGStore) Create(ctx context.Context, name string, kind Kind, endpoint, region string, encrypted []byte, credKeys []string) (ProviderRow, error) {
	if len(encrypted) == 0 {
		return ProviderRow{}, errors.New("cloud store: şifreli blob boş")
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM cloud_providers`).Scan(&n); err != nil {
		return ProviderRow{}, fmt.Errorf("cloud store: sayım: %w", err)
	}
	if n >= MaxProviders {
		return ProviderRow{}, fmt.Errorf("%w: en fazla %d", ErrProviderLimit, MaxProviders)
	}
	keysJSON := marshalKeys(credKeys)
	row := s.pool.QueryRow(ctx, `
INSERT INTO cloud_providers (name, kind, endpoint, region, credentials_encrypted, credential_keys)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at, updated_at`, name, string(kind), endpoint, region, encrypted, keysJSON)
	var out ProviderRow
	if err := row.Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return ProviderRow{}, fmt.Errorf("cloud store: ekleme: %w", err)
	}
	out.Name, out.Kind, out.Endpoint, out.Region, out.CredKeyNames = name, kind, endpoint, region, credKeys
	return out, nil
}

// List returns all providers (no credential material).
func (s *PGStore) List(ctx context.Context) ([]ProviderRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, name, kind, endpoint, region, credential_keys, created_at, updated_at
FROM cloud_providers ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("cloud store: liste: %w", err)
	}
	defer rows.Close()
	out := []ProviderRow{}
	for rows.Next() {
		var r ProviderRow
		var keysJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Endpoint, &r.Region, &keysJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.CredKeyNames = unmarshalKeys(keysJSON)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one provider row (no credential material).
func (s *PGStore) Get(ctx context.Context, id string) (ProviderRow, error) {
	var r ProviderRow
	var keysJSON []byte
	err := s.pool.QueryRow(ctx, `
SELECT id, name, kind, endpoint, region, credential_keys, created_at, updated_at
FROM cloud_providers WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.Kind, &r.Endpoint, &r.Region, &keysJSON, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderRow{}, ErrProviderNotFound
	}
	if err != nil {
		return ProviderRow{}, fmt.Errorf("cloud store: okuma: %w", err)
	}
	r.CredKeyNames = unmarshalKeys(keysJSON)
	return r, nil
}

// Delete removes the registration.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM cloud_providers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("cloud store: silme: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// EncryptedCredentials returns the sealed blob for executor-only decryption.
func (s *PGStore) EncryptedCredentials(ctx context.Context, id string) ([]byte, error) {
	var blob []byte
	err := s.pool.QueryRow(ctx,
		`SELECT credentials_encrypted FROM cloud_providers WHERE id = $1`, id).Scan(&blob)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cloud store: kimlik bilgisi okuma: %w", err)
	}
	return blob, nil
}
