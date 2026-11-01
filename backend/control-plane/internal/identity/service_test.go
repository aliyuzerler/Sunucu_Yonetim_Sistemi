package identity

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store for unit tests.
type fakeStore struct {
	mu            sync.Mutex
	users         []*User
	sessions      map[string]*Session // by TokenHash
	csrfBySession map[string]string   // sessionID -> csrf hash
	bootstrapHash string              // one pending token hash
	bootstrapUsed bool
	bootstrapExp  time.Time
	adminBindings map[string]bool
	failBootstrap bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:      map[string]*Session{},
		adminBindings: map[string]bool{},
	}
}

func (f *fakeStore) UsersExist(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.users) > 0, nil
}

func (f *fakeStore) CreateUser(_ context.Context, u *User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u.ID = "u-" + u.Username
	u.Status = "active"
	f.users = append(f.users, u)
	return nil
}

func (f *fakeStore) UserByUsername(_ context.Context, username string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Username, username) {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrInvalidCredentials
}

func (f *fakeStore) CreateAdminBinding(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adminBindings[userID] = true
	return nil
}

func (f *fakeStore) InsertBootstrapToken(_ context.Context, hash string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootstrapHash = hash
	f.bootstrapExp = expiresAt
	f.bootstrapUsed = false
	return nil
}

func (f *fakeStore) BootstrapAdmin(_ context.Context, tokenHash, username, passwordHash, displayName string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failBootstrap {
		return nil, errorsNew("bozuk")
	}
	if len(f.users) > 0 {
		return nil, ErrBootstrapUnavailable
	}
	if tokenHash != f.bootstrapHash || f.bootstrapUsed {
		return nil, ErrBootstrapTokenBad
	}
	if time.Now().After(f.bootstrapExp) {
		return nil, ErrBootstrapTokenExpired
	}
	f.bootstrapUsed = true
	u := &User{ID: "u-" + username, Username: username, DisplayName: displayName, Status: "active", PasswordHash: passwordHash}
	f.users = append(f.users, u)
	f.adminBindings[u.ID] = true
	return u, nil
}

func (f *fakeStore) CreateSession(_ context.Context, s *Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s.ID = "s-" + s.TokenHash[:8]
	f.sessions[s.TokenHash] = s
	return nil
}

func (f *fakeStore) SessionByTokenHash(_ context.Context, h string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[h]
	if !ok {
		return nil, ErrSessionInvalid
	}
	cp := *s
	return &cp, nil
}

func (f *fakeStore) TouchSession(context.Context, string) error { return nil }

func (f *fakeStore) RevokeSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for h, s := range f.sessions {
		if s.ID == id {
			delete(f.sessions, h)
		}
	}
	return nil
}

func (f *fakeStore) DeleteExpiredSessions(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (f *fakeStore) ListUsers(context.Context) ([]UserPublic, error) {
	out := []UserPublic{}
	for _, u := range f.users {
		out = append(out, UserPublic{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Status: u.Status})
	}
	return out, nil
}

func errorsNew(s string) error { return &simpleErr{s} }

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func newTestService(store Store) *Service {
	return NewService(store, Config{SessionTTL: time.Hour, BootstrapTTL: 15 * time.Minute})
}

func TestBootstrapLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newTestService(store)

	// İlk aşamada token verilebilir.
	tok, err := svc.IssueBootstrapToken(ctx)
	if err != nil {
		t.Fatalf("token verilemedi: %v", err)
	}
	if tok.Plain == "" || len(tok.Hash) != 64 {
		t.Fatalf("token alanları geçersiz")
	}

	// Admin yokken bootstrap başarılı.
	u, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{
		Token: tok.Plain, Username: "ali", Password: "guclu-parola-123", DisplayName: "Ali",
	})
	if err != nil {
		t.Fatalf("bootstrap başarısız: %v", err)
	}
	if !store.adminBindings[u.ID] {
		t.Fatal("admin rol bağlaması eksik")
	}

	// Kurulumdan sonra yeni token verilemez.
	if _, err := svc.IssueBootstrapToken(ctx); err != ErrBootstrapUnavailable {
		t.Fatalf("kurulum sonrası token verilmemeli: %v", err)
	}

	// Aynı token ikinci kez kullanılamaz (tek kullanımlık).
	_, err = svc.BootstrapAdmin(ctx, BootstrapAdminInput{
		Token: tok.Plain, Username: "ayse", Password: "guclu-parola-123",
	})
	if err == nil {
		t.Fatal("tek kullanımlık token tekrar kabul edildi")
	}
}

func TestBootstrapRejectsBadTokenAndUsername(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newTestService(store)

	if _, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: "yok", Username: "ali", Password: "guclu-parola-123"}); err == nil {
		t.Fatal("geçersiz token reddedilmeli")
	}
	tok, _ := svc.IssueBootstrapToken(ctx)
	if _, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: tok.Plain, Username: "GEÇERSİZ", Password: "guclu-parola-123"}); err != ErrInvalidUsername {
		t.Fatalf("geçersiz kullanıcı adı reddedilmeli: %v", err)
	}
	if _, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: tok.Plain, Username: "ali", Password: "kısa"}); err == nil {
		t.Fatal("kısa parola reddedilmeli")
	}
}

func TestLoginFlow(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newTestService(store)
	tok, _ := svc.IssueBootstrapToken(ctx)
	if _, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: tok.Plain, Username: "ali", Password: "guclu-parola-123"}); err != nil {
		t.Fatal(err)
	}

	// Yanlış parola: uniform hata.
	if _, err := svc.Login(ctx, LoginInput{Username: "ali", Password: "yanlis-parola-1", SourceIP: "10.0.0.1"}); err != ErrInvalidCredentials {
		t.Fatalf("uniform hata bekleniyordu: %v", err)
	}
	// Bilinmeyen kullanıcı: aynı uniform hata (kullanım sızıntısı yok).
	if _, err := svc.Login(ctx, LoginInput{Username: "yok", Password: "guclu-parola-123", SourceIP: "10.0.0.1"}); err != ErrInvalidCredentials {
		t.Fatalf("bilinmeyen kullanıcı uniform hata vermeli: %v", err)
	}

	// Başarılı giriş.
	res, err := svc.Login(ctx, LoginInput{Username: "ali", Password: "guclu-parola-123", SourceIP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("giriş başarısız: %v", err)
	}
	if res.Token == "" || res.CSRFToken == "" || res.Session.ID == "" {
		t.Fatal("giriş çıktıları eksik")
	}

	// Oturum çözümlemesi ve CSRF eşleşmesi.
	sess, err := svc.ResolveSession(ctx, res.Token)
	if err != nil {
		t.Fatalf("oturum çözülemedi: %v", err)
	}
	if !svc.CSRFMatches(res.Token, res.CSRFToken) {
		t.Fatal("CSRF eşleşmeli")
	}
	if svc.CSRFMatches(res.Token, "yanlis") {
		t.Fatal("yanlış CSRF kabul edildi")
	}

	// Logout sonrası oturum geçersiz.
	if err := svc.Logout(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSession(ctx, res.Token); err != ErrSessionInvalid {
		t.Fatalf("logout sonrası oturum geçersiz olmalı: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newTestService(store)
	tok, _ := svc.IssueBootstrapToken(ctx)
	svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: tok.Plain, Username: "ali", Password: "guclu-parola-123"})

	// 8 deneme limiti: 8 hatalı deneme sonrası blok.
	for i := 0; i < 8; i++ {
		if _, err := svc.Login(ctx, LoginInput{Username: "ali", Password: "yanlis-parola-1", SourceIP: "10.9.9.9"}); err != ErrInvalidCredentials {
			t.Fatalf("deneme %d: uniform hata bekleniyordu: %v", i+1, err)
		}
	}
	if _, err := svc.Login(ctx, LoginInput{Username: "ali", Password: "guclu-parola-123", SourceIP: "10.9.9.9"}); err != ErrTooManyAttempts {
		t.Fatalf("rate limit devreye girmeli: %v", err)
	}

	// Başarılı giriş limiterı resetler (yeni servis).
	svc2 := newTestService(store)
	if _, err := svc2.Login(ctx, LoginInput{Username: "ali", Password: "guclu-parola-123", SourceIP: "10.0.0.2"}); err != nil {
		t.Fatalf("taze limiterda giriş başarılı olmalı: %v", err)
	}
}

func TestLoginDisabledUser(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newTestService(store)
	tok, _ := svc.IssueBootstrapToken(ctx)
	svc.BootstrapAdmin(ctx, BootstrapAdminInput{Token: tok.Plain, Username: "ali", Password: "guclu-parola-123"})
	store.mu.Lock()
	store.users[0].Status = "disabled"
	store.mu.Unlock()

	if _, err := svc.Login(ctx, LoginInput{Username: "ali", Password: "guclu-parola-123", SourceIP: "10.0.0.3"}); err != ErrUserDisabled {
		t.Fatalf("devre dışı kullanıcı reddedilmeli: %v", err)
	}
}

func TestRateLimiterWindow(t *testing.T) {
	l := NewRateLimiter(2, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }

	if !l.Allow("k") || !l.Allow("k") {
		t.Fatal("ilk iki deneme izinli olmalı")
	}
	if l.Allow("k") {
		t.Fatal("üçüncü deneme bloklanmalı")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("k") {
		t.Fatal("pencere sıfırlanınca izin verilmeli")
	}
}
