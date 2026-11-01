package identity

import (
	"context"
	"testing"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/testdb"
)

// TestBootstrapAndSessionsPG covers the full first-run flow on PostgreSQL:
// one-time token issuance, single-use consumption, admin binding, sessions.
func TestBootstrapAndSessionsPG(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx := context.Background()

	store := NewPGStore(pool)
	svc := NewService(store, Config{SessionTTL: time.Hour, BootstrapTTL: 15 * time.Minute})

	exists, err := store.UsersExist(ctx)
	if err != nil || exists {
		t.Fatalf("temiz veritabanında kullanıcı olmamalı: %v %v", exists, err)
	}

	// Token üretimi.
	tok, err := svc.IssueBootstrapToken(ctx)
	if err != nil {
		t.Fatalf("token üretilemedi: %v", err)
	}

	// Bootstrap: kullanıcı + admin bağlamı tek transaction'da.
	u, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{
		Token: tok.Plain, Username: "kurulum-admin", Password: "cok-gizli-parola-42",
		DisplayName: "Kurulum Yöneticisi",
	})
	if err != nil {
		t.Fatalf("bootstrap başarısız: %v", err)
	}

	// Rol bağlamı: admin + tüm izinler + global scope.
	rbacStore := rbac.NewPGStore(pool)
	bindings, err := rbacStore.BindingsForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("bağlamalar okunamadı: %v", err)
	}
	if len(bindings) != 1 || bindings[0].RoleName != "admin" || bindings[0].ScopeType != rbac.ScopeAll {
		t.Fatalf("admin bağlaması bekleniyordu: %+v", bindings)
	}
	if len(bindings[0].Permissions) != len(rbac.AllPermissions) {
		t.Fatalf("admin tüm izinlere sahip olmalı: %v", bindings[0].Permissions)
	}

	// Tek kullanımlık: aynı token tekrar reddedilir.
	if _, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{
		Token: tok.Plain, Username: "ikinci-admin", Password: "cok-gizli-parola-42",
	}); err == nil {
		t.Fatal("aynı token ikinci kez kabul edilmemeli")
	}

	// Kurulum bittikten sonra yeni token verilmez.
	if _, err := svc.IssueBootstrapToken(ctx); err != ErrBootstrapUnavailable {
		t.Fatalf("kurulum sonrası token verilmemeli: %v", err)
	}

	// Giriş: yanlış parola uniform hata; doğru parola oturum açar.
	if _, err := svc.Login(ctx, LoginInput{Username: "kurulum-admin", Password: "yanlis-parola-99", SourceIP: "10.1.1.1"}); err != ErrInvalidCredentials {
		t.Fatalf("uniform hata bekleniyordu: %v", err)
	}
	res, err := svc.Login(ctx, LoginInput{Username: "kurulum-admin", Password: "cok-gizli-parola-42", SourceIP: "10.1.1.1"})
	if err != nil {
		t.Fatalf("giriş başarısız: %v", err)
	}

	// Oturum çözümlemesi + CSRF + logout.
	sess, err := svc.ResolveSession(ctx, res.Token)
	if err != nil {
		t.Fatalf("oturum çözülemedi: %v", err)
	}
	if !svc.CSRFMatches(res.Token, res.CSRFToken) {
		t.Fatal("CSRF eşleşmeli")
	}
	if svc.CSRFMatches(res.Token, "baskasi") {
		t.Fatal("yanlış CSRF reddedilmeli")
	}
	if err := svc.Logout(ctx, sess.ID); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.ResolveSession(ctx, res.Token); err != ErrSessionInvalid {
		t.Fatalf("logout sonrası oturum geçersiz olmalı: %v", err)
	}

	// DB'de token düz metin saklanmamalı.
	n := testdb.QueryInt(t, pool,
		`SELECT count(*) FROM sessions WHERE token_hash = $1`, res.Token)
	if n != 0 {
		t.Fatal("oturum token düz metini DB'ye yazılmamalı")
	}
}

// TestBootstrapRacePG proves single-use under concurrency: parallel bootstrap
// attempts with the same token yield exactly one user.
func TestBootstrapRacePG(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Reset(t, pool)
	ctx := context.Background()

	store := NewPGStore(pool)
	svc := NewService(store, Config{})

	tok, err := svc.IssueBootstrapToken(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const racers = 8
	errCh := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			_, err := svc.BootstrapAdmin(ctx, BootstrapAdminInput{
				Token: tok.Plain, Username: "yariscilar", Password: "cok-gizli-parola-42",
			})
			errCh <- err
		}()
	}
	successes := 0
	for i := 0; i < racers; i++ {
		if err := <-errCh; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("yarışta tam bir bootstrap bekleniyordu: %d", successes)
	}
	if n := testdb.QueryInt(t, pool, `SELECT count(*) FROM users`); n != 1 {
		t.Fatalf("tek kullanıcı bekleniyordu: %d", n)
	}
}
