package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// User is an identity record. PasswordHash is never serialized to JSON.
type User struct {
	ID           string
	Username     string
	DisplayName  string
	Status       string // active | disabled
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

// Session is a server-side session row. The token plaintext is never stored;
// only its SHA-256 digest is persisted (TokenHash, not JSON serialized).
type Session struct {
	ID        string
	UserID    string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
	TokenHash string `json:"-"`
}

// Store is the persistence contract for identity. SQL implementation lives in
// store_pg.go; tests may substitute fakes.
type Store interface {
	UsersExist(ctx context.Context) (bool, error)
	CreateUser(ctx context.Context, u *User) error
	UserByUsername(ctx context.Context, username string) (*User, error)
	CreateAdminBinding(ctx context.Context, userID string) error
	InsertBootstrapToken(ctx context.Context, hash string, expiresAt time.Time) error
	BootstrapAdmin(ctx context.Context, tokenHash, username, passwordHash, displayName string) (*User, error)
	CreateSession(ctx context.Context, s *Session) error
	SessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	ListUsers(ctx context.Context) ([]UserPublic, error)
	TouchSession(ctx context.Context, id string) error
	RevokeSession(ctx context.Context, id string) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error)
}

// Service implements identity use cases on top of a Store.
type Service struct {
	store        Store
	sessionTTL   time.Duration
	bootstrapTTL time.Duration
	loginLimiter *RateLimiter
	ipLimiter    *RateLimiter
	now          func() time.Time
}

var usernameRE = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

// Errors returned by the service; handlers map them to stable API codes.
var (
	ErrBootstrapUnavailable = errors.New("bootstrap kullanılamaz: kurulum zaten tamamlanmış")
	ErrBootstrapTokenBad    = errors.New("bootstrap token geçersiz")
	ErrSessionInvalid       = errors.New("oturum geçersiz veya süresi dolmuş")
	ErrUserDisabled         = errors.New("kullanıcı devre dışı")
	ErrTooManyAttempts      = errors.New("çok fazla deneme; sonra tekrar deneyin")
	ErrInvalidUsername      = errors.New("kullanıcı adı biçimi geçersiz")
)

// Config tunes the service.
type Config struct {
	SessionTTL   time.Duration // default 12h
	BootstrapTTL time.Duration // default 15m
}

// NewService builds the identity service with safe defaults.
func NewService(store Store, cfg Config) *Service {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.BootstrapTTL <= 0 {
		cfg.BootstrapTTL = 15 * time.Minute
	}
	return &Service{
		store:        store,
		sessionTTL:   cfg.SessionTTL,
		bootstrapTTL: cfg.BootstrapTTL,
		loginLimiter: NewRateLimiter(8, 5*time.Minute),  // per username
		ipLimiter:    NewRateLimiter(20, 5*time.Minute), // per source IP
		now:          time.Now,
	}
}

// IssueBootstrapToken creates a single-use, short-lived bootstrap token.
// It fails once any user exists: the token is only for first-run setup.
func (s *Service) IssueBootstrapToken(ctx context.Context) (Token, error) {
	exists, err := s.store.UsersExist(ctx)
	if err != nil {
		return Token{}, fmt.Errorf("kullanıcı denetimi: %w", err)
	}
	if exists {
		return Token{}, ErrBootstrapUnavailable
	}
	tok, err := NewToken()
	if err != nil {
		return Token{}, err
	}
	if err := s.store.InsertBootstrapToken(ctx, tok.Hash, s.now().Add(s.bootstrapTTL)); err != nil {
		return Token{}, fmt.Errorf("token kaydedilemedi: %w", err)
	}
	return tok, nil
}

// BootstrapAdminInput is the request payload for first-admin creation.
type BootstrapAdminInput struct {
	Token       string
	Username    string
	Password    string
	DisplayName string
}

// BootstrapAdmin consumes the one-time token and creates the first admin in
// one database transaction (token consumption + user + role binding). There
// is no default password anywhere: the caller supplies the only credential.
func (s *Service) BootstrapAdmin(ctx context.Context, in BootstrapAdminInput) (*User, error) {
	if !usernameRE.MatchString(in.Username) {
		return nil, ErrInvalidUsername
	}
	if in.Token == "" {
		return nil, ErrBootstrapTokenBad
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u, err := s.store.BootstrapAdmin(ctx, HashToken(in.Token), in.Username, hash, in.DisplayName)
	if err != nil {
		return nil, err
	}
	return &User{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Status: u.Status}, nil
}

// ResolveSession validates a presented session token and returns the live
// session. Expired/revoked tokens collapse into ErrSessionInvalid.
func (s *Service) ResolveSession(ctx context.Context, tokenPlain string) (*Session, error) {
	if tokenPlain == "" {
		return nil, ErrSessionInvalid
	}
	sess, err := s.store.SessionByTokenHash(ctx, HashToken(tokenPlain))
	if err != nil {
		return nil, ErrSessionInvalid
	}
	// Touch is best-effort: a failed update must not fail the request.
	_ = s.store.TouchSession(ctx, sess.ID)
	return sess, nil
}

// CSRFMatches reports whether the presented CSRF token equals the value
// derived from the session token (constant-time comparison).
func (s *Service) CSRFMatches(sessionToken, presented string) bool {
	if sessionToken == "" || presented == "" {
		return false
	}
	want := []byte(DeriveCSRF(sessionToken))
	got := []byte(presented)
	return subtle.ConstantTimeCompare(want, got) == 1
}

// Logout revokes the session identified by its ID.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.store.RevokeSession(ctx, sessionID)
}

// Login verifies credentials and creates a session. It returns the session
// token and CSRF token plaintexts (to be delivered to the browser once).
type LoginResult struct {
	Session   Session
	Token     string // session token plaintext (cookie value)
	CSRFToken string // CSRF token plaintext (client-side header value)
}

// LoginInput carries authentication attempt data.
type LoginInput struct {
	Username string
	Password string
	SourceIP string
}

// Login enforces rate limits, verifies credentials and creates the session.
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	userKey := "user:" + in.Username
	ipKey := "ip:" + in.SourceIP

	if !s.ipLimiter.Allow(ipKey) || !s.loginLimiter.Allow(userKey) {
		return nil, ErrTooManyAttempts
	}

	u, err := s.store.UserByUsername(ctx, in.Username)
	if err != nil {
		// Uniform failure: same error for unknown user and wrong password.
		return nil, ErrInvalidCredentials
	}
	if u.Status != "active" {
		return nil, ErrUserDisabled
	}
	ok, err := VerifyPassword(in.Password, u.PasswordHash)
	if err != nil || !ok {
		return nil, ErrInvalidCredentials
	}

	s.loginLimiter.Reset(userKey)
	s.ipLimiter.Reset(ipKey)

	tok, err := NewToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{
		UserID:    u.ID,
		Username:  u.Username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.sessionTTL),
		TokenHash: tok.Hash,
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("oturum oluşturulamadı: %w", err)
	}
	return &LoginResult{Session: *sess, Token: tok.Plain, CSRFToken: DeriveCSRF(tok.Plain)}, nil
}

// SessionTTL returns the configured session lifetime (cookie MaxAge).
func (s *Service) SessionTTL() time.Duration { return s.sessionTTL }

// UserPublic is the API-safe user projection (no password material).
type UserPublic struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// ListUsers returns the user accounts (read-only view).
func (s *Service) ListUsers(ctx context.Context) ([]UserPublic, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	if users == nil {
		users = []UserPublic{}
	}
	return users, nil
}
