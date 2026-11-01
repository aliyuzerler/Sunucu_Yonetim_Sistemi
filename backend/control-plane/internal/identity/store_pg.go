package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// Sentinel errors mapped by handlers; SQL layer wraps them with context.
var (
	ErrBootstrapTokenConsumed = errors.New("bootstrap token zaten kullanılmış")
	ErrBootstrapTokenExpired  = errors.New("bootstrap token süresi dolmuş")
)

// UsersExist reports whether any user account exists (first-run gate).
func (s *PGStore) UsersExist(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("users varlık denetimi: %w", err)
	}
	return exists, nil
}

// CreateUser inserts a new user row.
func (s *PGStore) CreateUser(ctx context.Context, u *User) error {
	const q = `
INSERT INTO users (username, display_name, password_hash)
VALUES ($1, $2, $3)
RETURNING id, status, created_at`
	err := s.pool.QueryRow(ctx, q, u.Username, u.DisplayName, u.PasswordHash).
		Scan(&u.ID, &u.Status, &u.CreatedAt)
	if err != nil {
		return fmt.Errorf("kullanıcı oluşturulamadı: %w", err)
	}
	return nil
}

// UserByUsername fetches an active-or-disabled user by (case-insensitive) name.
func (s *PGStore) UserByUsername(ctx context.Context, username string) (*User, error) {
	const q = `
SELECT id, username, display_name, status, password_hash, created_at, last_login_at
FROM users WHERE lower(username) = lower($1)`
	u := &User{}
	err := s.pool.QueryRow(ctx, q, username).Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.Status, &u.PasswordHash, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("kullanıcı sorgusu: %w", err)
	}
	return u, nil
}

// CreateAdminBinding binds the user to the system admin role with global scope.
func (s *PGStore) CreateAdminBinding(ctx context.Context, userID string) error {
	const q = `
INSERT INTO user_role_bindings (user_id, role_id, scope_type)
SELECT $1, id, 'all' FROM roles WHERE name = 'admin'`
	ct, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("admin rol bağlaması: %w", err)
	}
	if ct.RowsAffected() != 1 {
		return errors.New("admin rolü bulunamadı (seed eksik)")
	}
	return nil
}

// InsertBootstrapToken stores a hashed one-time bootstrap token.
func (s *PGStore) InsertBootstrapToken(ctx context.Context, hash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO admin_bootstrap_tokens (token_hash, expires_at) VALUES ($1, $2)`,
		hash, expiresAt)
	if err != nil {
		return fmt.Errorf("bootstrap token kaydı: %w", err)
	}
	return nil
}

// BootstrapAdmin consumes a one-time token and creates the first admin user
// plus its global admin role binding in a SINGLE transaction. Any failure
// rolls everything back, including token consumption.
func (s *PGStore) BootstrapAdmin(ctx context.Context, tokenHash, username, passwordHash, displayName string) (*User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("transaction başlatılamadı: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenID string
	var expiresAt, consumedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, expires_at, consumed_at FROM admin_bootstrap_tokens
          WHERE token_hash = $1 FOR UPDATE`, tokenHash).Scan(&tokenID, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBootstrapTokenBad
	}
	if err != nil {
		return nil, fmt.Errorf("bootstrap token sorgusu: %w", err)
	}
	if consumedAt != nil {
		return nil, ErrBootstrapTokenConsumed
	}
	if expiresAt == nil || time.Now().After(*expiresAt) {
		return nil, ErrBootstrapTokenExpired
	}

	// First-run gate inside the same tx prevents bootstrap races.
	var userCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		return nil, fmt.Errorf("kullanıcı sayımı: %w", err)
	}
	if userCount > 0 {
		return nil, ErrBootstrapUnavailable
	}

	u := &User{Username: username, DisplayName: displayName, PasswordHash: passwordHash}
	err = tx.QueryRow(ctx,
		`INSERT INTO users (username, display_name, password_hash)
                 VALUES ($1, $2, $3) RETURNING id, status, created_at`,
		u.Username, u.DisplayName, u.PasswordHash).Scan(&u.ID, &u.Status, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("ilk yönetici oluşturulamadı: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_role_bindings (user_id, role_id, scope_type)
                 SELECT $1, id, 'all' FROM roles WHERE name = 'admin'`, u.ID); err != nil {
		return nil, fmt.Errorf("admin rol bağlaması: %w", err)
	}

	ct, err := tx.Exec(ctx,
		`UPDATE admin_bootstrap_tokens SET consumed_at = now() WHERE id = $1 AND consumed_at IS NULL`,
		tokenID)
	if err != nil {
		return nil, fmt.Errorf("token tüketimi: %w", err)
	}
	if ct.RowsAffected() != 1 {
		return nil, ErrBootstrapTokenConsumed
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap transaction: %w", err)
	}
	return u, nil
}

// CreateSession persists the session and stamps login time. CSRF tokens are
// derived from the session token and never stored.
func (s *PGStore) CreateSession(ctx context.Context, sess *Session) error {
	const q = `
INSERT INTO sessions (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, created_at`
	err := s.pool.QueryRow(ctx, q,
		sess.UserID, sess.TokenHash, sess.ExpiresAt).
		Scan(&sess.ID, &sess.CreatedAt)
	if err != nil {
		return fmt.Errorf("oturum kaydı: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE users SET last_login_at = now() WHERE id = $1`, sess.UserID); err != nil {
		return fmt.Errorf("son giriş güncellenemedi: %w", err)
	}
	return nil
}

// SessionByTokenHash resolves a live session by token digest.
func (s *PGStore) SessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	const q = `
SELECT s.id, s.user_id, u.username, s.created_at, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.status = 'active'`
	sess := &Session{}
	err := s.pool.QueryRow(ctx, q, tokenHash).Scan(
		&sess.ID, &sess.UserID, &sess.Username, &sess.CreatedAt, &sess.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("oturum sorgusu: %w", err)
	}
	return sess, nil
}

// TouchSession updates last_seen_at for the session.
func (s *PGStore) TouchSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("oturum güncelleme: %w", err)
	}
	return nil
}

// RevokeSession marks the session revoked (logout).
func (s *PGStore) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("oturum iptali: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes expired and long-revoked sessions.
func (s *PGStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
DELETE FROM sessions
WHERE expires_at < $1
   OR (revoked_at IS NOT NULL AND revoked_at < $1 - interval '7 days')`, now)
	if err != nil {
		return 0, fmt.Errorf("oturum temizliği: %w", err)
	}
	return ct.RowsAffected(), nil
}

// ListUsers returns API-safe user projections.
func (s *PGStore) ListUsers(ctx context.Context) ([]UserPublic, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, username, display_name, status, created_at, last_login_at
FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("kullanıcı listesi: %w", err)
	}
	defer rows.Close()
	var out []UserPublic
	for rows.Next() {
		var u UserPublic
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Status, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, fmt.Errorf("kullanıcı satırı: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
