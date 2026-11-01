package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProviderRow is the persisted provider registration. Credentials are
// stored ONLY as AES-256-GCM ciphertext (internal/secrets); no read path
// ever exposes them except the executor-only GetEncryptedCredentials.
type ProviderRow struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	Capabilities   Capabilities `json:"capabilities"`
	HasCredentials bool         `json:"has_credentials"`
	CredKeyNames   []string     `json:"credential_keys"` // yalnız ANAHTAR ADLARI (değer asla)
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

var (
	ErrProviderNotFound = errors.New("dns: sağlayıcı bulunamadı")
	ErrProviderLimit    = errors.New("dns: sağlayıcı sınırına ulaşıldı")
)

// Store persists provider registrations (PostgreSQL, Faz 09).
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create inserts one provider. encrypted is the already-sealed credential
// blob (sealing is the service layer's duty via secrets.Key). Creation is
// credential-mandatory: a provider without credentials is refused earlier.
func (s *Store) Create(ctx context.Context, name, kind string, caps Capabilities, encrypted []byte, credKeys []string) (ProviderRow, error) {
	if len(encrypted) == 0 {
		return ProviderRow{}, errors.New("dns store: kimlik bilgisi şifreli blob'u boş")
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM dns_providers`).Scan(&n); err != nil {
		return ProviderRow{}, fmt.Errorf("dns store: sayım: %w", err)
	}
	if n >= MaxProviders {
		return ProviderRow{}, fmt.Errorf("%w: en fazla %d", ErrProviderLimit, MaxProviders)
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return ProviderRow{}, err
	}
	keysJSON, err := json.Marshal(credKeys)
	if err != nil {
		return ProviderRow{}, err
	}
	row := s.pool.QueryRow(ctx, `
                INSERT INTO dns_providers (name, kind, capabilities, credentials_encrypted, credential_keys)
                VALUES ($1, $2, $3, $4, $5)
                RETURNING id, created_at, updated_at`,
		name, kind, capsJSON, encrypted, keysJSON)
	var out ProviderRow
	if err := row.Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return ProviderRow{}, fmt.Errorf("dns store: ekleme: %w", err)
	}
	out.Name, out.Kind, out.HasCredentials = name, kind, true
	out.Capabilities = caps
	out.CredKeyNames = credKeys
	return out, nil
}

// List returns all providers WITHOUT credential material (only key names).
func (s *Store) List(ctx context.Context) ([]ProviderRow, error) {
	rows, err := s.pool.Query(ctx, `
                SELECT id, name, kind, capabilities, credential_keys, created_at, updated_at
                FROM dns_providers ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("dns store: liste: %w", err)
	}
	defer rows.Close()
	var out []ProviderRow
	for rows.Next() {
		var r ProviderRow
		var capsJSON, keysJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &capsJSON, &keysJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capsJSON, &r.Capabilities); err != nil {
			return nil, fmt.Errorf("dns store: capabilities bozuk: %w", err)
		}
		if err := json.Unmarshal(keysJSON, &r.CredKeyNames); err != nil {
			return nil, fmt.Errorf("dns store: credential_keys bozuk: %w", err)
		}
		r.HasCredentials = true
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one provider row (no credential material).
func (s *Store) Get(ctx context.Context, id string) (ProviderRow, error) {
	var r ProviderRow
	var capsJSON, keysJSON []byte
	err := s.pool.QueryRow(ctx, `
                SELECT id, name, kind, capabilities, credential_keys, created_at, updated_at
                FROM dns_providers WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.Kind, &capsJSON, &keysJSON, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderRow{}, ErrProviderNotFound
	}
	if err != nil {
		return ProviderRow{}, fmt.Errorf("dns store: okuma: %w", err)
	}
	if err := json.Unmarshal(capsJSON, &r.Capabilities); err != nil {
		return ProviderRow{}, fmt.Errorf("dns store: capabilities bozuk: %w", err)
	}
	if err := json.Unmarshal(keysJSON, &r.CredKeyNames); err != nil {
		return ProviderRow{}, fmt.Errorf("dns store: credential_keys bozuk: %w", err)
	}
	r.HasCredentials = true
	return r, nil
}

// GetEncryptedCredentials returns the sealed blob — ONLY for the executor
// path (build provider client). Never called by HTTP handlers.
func (s *Store) GetEncryptedCredentials(ctx context.Context, id string) ([]byte, error) {
	var sealed []byte
	err := s.pool.QueryRow(ctx, `
                SELECT credentials_encrypted FROM dns_providers WHERE id = $1`, id).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProviderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dns store: kimlik bilgisi okuma: %w", err)
	}
	return sealed, nil
}

// Delete removes a provider registration.
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM dns_providers WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("dns store: silme: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProviderNotFound
	}
	return nil
}
