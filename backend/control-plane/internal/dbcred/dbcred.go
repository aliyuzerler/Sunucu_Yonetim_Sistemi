// Package dbcred implements the Faz 10 database-credential lifecycle.
//
// Security model (ADR-0014, docs/architecture/db-files.md):
//   - DB user passwords are generated ON THE NODE (dbops, crypto/rand) and
//     arrive only inside the db.user.create task result.
//   - Before that result is persisted, the agentsrv ResultScrubber strips
//     the password from the result map and stores it here as an
//     AES-256-GCM sealed blob (internal/secrets, CP_SECRETS_KEY). The
//     plaintext password NEVER lives in tasks.result, audit records, logs
//     or outbox events.
//   - Reveal is ONE-TIME: a single UPDATE ... WHERE revealed_at IS NULL
//     RETURNING race-free statement; the second reveal is a typed error.
//   - Reads (list/get) never return the sealed blob.
package dbcred

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation names mirrored from dbops/ops_faz10 (scrubber triggers).
const (
	OpDBUserCreate = "db.user.create"
	OpDBUserDelete = "db.user.delete"
)

// Stable errors.
var (
	// ErrNotFound reports an unknown credential id.
	ErrNotFound = errors.New("dbcred: kimlik bulunamadı")
	// ErrAlreadyRevealed reports the one-time reveal already happened.
	ErrAlreadyRevealed = errors.New("dbcred: şifre zaten bir kez gösterildi")
	// ErrKeyNotConfigured reports CP_SECRETS_KEY is absent: no seal, no reveal.
	ErrKeyNotConfigured = errors.New("dbcred: CP_SECRETS_KEY yapılandırılmamış")
)

// Credential is the persisted credential row (NEVER carries the plaintext
// or the sealed blob — reveal decrypts transiently).
type Credential struct {
	ID          string     `json:"id"`
	NodeID      string     `json:"node_id"`
	Kind        string     `json:"kind"`
	Username    string     `json:"username"`
	Host        string     `json:"host,omitempty"`
	Fingerprint string     `json:"fingerprint"`
	RevealedAt  *time.Time `json:"revealed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Store persists credentials (PostgreSQL).
type Store struct{ pool *pgxpool.Pool }

// NewStore builds the store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const credCols = `id, node_id::text, kind, username, host, fingerprint,
revealed_at, created_at, updated_at`

func scanCred(row pgx.Row) (Credential, error) {
	var c Credential
	err := row.Scan(&c.ID, &c.NodeID, &c.Kind, &c.Username, &c.Host,
		&c.Fingerprint, &c.RevealedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("dbcred store: tarama: %w", err)
	}
	return c, nil
}

// Upsert seals one credential for (node, kind, username, host). A repeated
// db.user.create for the same user replaces the blob and RESETS the
// one-time reveal window (the old password no longer exists).
func (s *Store) Upsert(ctx context.Context, nodeID, kind, username, host string, sealed []byte, fingerprint string) (Credential, error) {
	if len(sealed) == 0 {
		return Credential{}, errors.New("dbcred store: sealed blob boş")
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO db_credentials (node_id, kind, username, host, password_encrypted, fingerprint)
VALUES ($1::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (node_id, kind, username, host)
DO UPDATE SET password_encrypted = EXCLUDED.password_encrypted,
              fingerprint        = EXCLUDED.fingerprint,
              revealed_at        = NULL,
              updated_at         = now()
RETURNING `+credCols, nodeID, kind, username, host, sealed, fingerprint)
	return scanCred(row)
}

// Reveal flips the one-time reveal marker and returns the sealed blob plus
// the credential row in ONE race-free statement. found=false → unknown id;
// already=true → second reveal attempt.
func (s *Store) Reveal(ctx context.Context, id string) (sealed []byte, cred Credential, found bool, already bool, err error) {
	row := s.pool.QueryRow(ctx, `
UPDATE db_credentials SET revealed_at = now(), updated_at = now()
WHERE id = $1::uuid AND revealed_at IS NULL
RETURNING password_encrypted, `+credCols, id)
	err = row.Scan(&sealed, &cred.ID, &cred.NodeID, &cred.Kind, &cred.Username,
		&cred.Host, &cred.Fingerprint, &cred.RevealedAt, &cred.CreatedAt, &cred.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Distinguish unknown id vs already-revealed.
		var n int
		nerr := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM db_credentials WHERE id = $1::uuid`, id).Scan(&n)
		if nerr != nil {
			return nil, Credential{}, false, false, fmt.Errorf("dbcred store: sayım: %w", nerr)
		}
		if n == 0 {
			return nil, Credential{}, false, false, nil
		}
		return nil, Credential{}, true, true, nil
	case err != nil:
		return nil, Credential{}, false, false, fmt.Errorf("dbcred store: reveal: %w", err)
	}
	return sealed, cred, true, false, nil
}

// List returns the node's credential rows (no blobs).
func (s *Store) List(ctx context.Context, nodeID string) ([]Credential, error) {
	if len(nodeID) > 64 {
		return nil, errors.New("dbcred store: node_id sınırı")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+credCols+`
FROM db_credentials WHERE node_id = $1::uuid ORDER BY created_at DESC LIMIT 500`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("dbcred store: liste: %w", err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.NodeID, &c.Kind, &c.Username, &c.Host,
			&c.Fingerprint, &c.RevealedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("dbcred store: satır: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteForUser removes credentials of a deleted DB user (the password has
// no owner anymore; keeping sealed bytes would be pointless retention).
func (s *Store) DeleteForUser(ctx context.Context, nodeID, kind, username string) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM db_credentials
WHERE node_id = $1::uuid AND kind = $2 AND username = $3`, nodeID, kind, username)
	if err != nil {
		return 0, fmt.Errorf("dbcred store: sil: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Fingerprint derives the display fingerprint of a password (first 16 hex
// of SHA-256) — enough to recognize "is this the password I saved?",
// useless for recovery.
func Fingerprint(pw string) string {
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])[:16]
}

// sealStore is the storage surface the service needs (interface keeps the
// scrub logic unit-testable without PostgreSQL).
type sealStore interface {
	Upsert(ctx context.Context, nodeID, kind, username, host string, sealed []byte, fingerprint string) (Credential, error)
	DeleteForUser(ctx context.Context, nodeID, kind, username string) (int, error)
	Reveal(ctx context.Context, id string) (sealed []byte, cred Credential, found bool, already bool, err error)
	List(ctx context.Context, nodeID string) ([]Credential, error)
}

// List delegates to the store (REST db:read surface — no blobs).
func (s *Service) List(ctx context.Context, nodeID string) ([]Credential, error) {
	return s.Store.List(ctx, nodeID)
}

// Service seals/scrubs/reveals credentials. With KeyConfigured=false it
// degrades HONESTLY: passwords are redacted from results (nothing is
// persisted, nothing can be revealed) — never stored in plaintext.
type Service struct {
	Store         sealStore
	Key           secrets.Key
	KeyConfigured bool
}

// ScrubNodeResult rewrites a db.* node result BEFORE persistence:
//   - db.user.create: extract credentials.password → seal+upsert → strip
//     from result, add credential_id/fingerprint/one_time_reveal.
//   - db.user.delete: drop the user's credential rows.
//
// With KeyConfigured=false (or on any failure) the password is replaced by
// an explicit redaction marker + honest reason: the operator learns the
// password CANNOT be retrieved and must re-create the user after
// configuring CP_SECRETS_KEY. Returning an error here would drop the whole
// result or worse, persist it unscrubbed; redaction must always win over
// retention (fail-closed).
func (s *Service) ScrubNodeResult(ctx context.Context, t *tasks.Task, result map[string]any) error {
	if t == nil || result == nil {
		return nil
	}
	switch t.Operation {
	case OpDBUserCreate:
		cred, _ := result["credentials"].(map[string]any)
		if cred == nil {
			return nil // no credentials block (already-exists replay) — nothing to seal
		}
		pw, _ := cred["password"].(string)
		if pw == "" {
			return nil
		}
		// Redact FIRST — every early-return path below keeps the plaintext
		// out of the persisted result no matter what sealing does.
		delete(cred, "password")
		cred["password_redacted"] = "true"
		kind, _ := result["kind"].(string)
		user, _ := result["user"].(map[string]any)
		username, _ := user["name"].(string)
		host, _ := user["host"].(string)
		if t.NodeID == nil || kind == "" || username == "" {
			cred["redaction_reason"] = "eksik provenance (node/kind/username) — kimlik saklanamadı"
			return nil
		}
		if !s.KeyConfigured {
			cred["redaction_reason"] = "CP_SECRETS_KEY yapılandırılmamış — şifre kalıcı saklanamadı; kullanıcıyı yeniden oluşturun"
			return nil
		}
		sealed, err := s.Key.Encrypt([]byte(pw))
		if err != nil {
			cred["redaction_reason"] = "seal başarısız — şifre kalıcı saklanamadı"
			return nil
		}
		c, err := s.Store.Upsert(ctx, *t.NodeID, kind, username, host, sealed, Fingerprint(pw))
		if err != nil {
			cred["redaction_reason"] = "kimlik kaydı yazılamadı — şifre kalıcı saklanamadı"
			return nil
		}
		delete(cred, "password_redacted")
		cred["credential_id"] = c.ID
		cred["fingerprint"] = c.Fingerprint
		cred["one_time_reveal"] = true
		return nil
	case OpDBUserDelete:
		if t.NodeID == nil {
			return nil
		}
		kind, _ := result["kind"].(string)
		user, _ := result["user"].(map[string]any)
		username, _ := user["name"].(string)
		if kind == "" || username == "" {
			return nil
		}
		_, _ = s.Store.DeleteForUser(ctx, *t.NodeID, kind, username)
		result["credentials_removed"] = true
		return nil
	}
	return nil
}

// Reveal decrypts the sealed password exactly once. Returns the plaintext
// (transient, caller must not log it) and the credential metadata.
func (s *Service) Reveal(ctx context.Context, id string) (string, Credential, error) {
	if !s.KeyConfigured {
		return "", Credential{}, ErrKeyNotConfigured
	}
	sealed, cred, found, already, err := s.Store.Reveal(ctx, id)
	if err != nil {
		return "", Credential{}, err
	}
	if !found {
		return "", Credential{}, ErrNotFound
	}
	if already {
		return "", Credential{}, ErrAlreadyRevealed
	}
	plain, err := s.Key.Decrypt(sealed)
	if err != nil {
		return "", Credential{}, fmt.Errorf("dbcred: çöz: %w", err)
	}
	return string(plain), cred, nil
}
