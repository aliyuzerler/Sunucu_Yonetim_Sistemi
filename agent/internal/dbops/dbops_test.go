//go:build linux

package dbops

// Birim testleri — gerçek veritabanı gerekmez: istemci ikili dosyaları
// enjekte edilir (sahte sh betikleri), soket/yedek dizinleri temp'tir.
// Doğrulama, SQL üretimi, korumalı adlar, şifre üretimi, yedek marker
// disiplini ve negatif güvenlik yolları burada kanıtlanır.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- doğrulama katmanı ----

func TestValidName(t *testing.T) {
	valid := []string{"app", "app_db", "a", "x9y", strings.Repeat("a", 63)}
	invalid := []string{"", "App", "9app", "_app", "app-db", "app db", "app;drop",
		"app'quote", strings.Repeat("a", 64), "app\"quote", "APp"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("geçerli sayılmalı: %q", n)
		}
	}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("reddedilmeli: %q", n)
		}
	}
}

func TestValidHost(t *testing.T) {
	if !ValidHost("localhost") || !ValidHost("%") || !ValidHost("db01.example.com") {
		t.Fatal("geçerli host'lar kabul edilmeli")
	}
	for _, h := range []string{"", "ho st", "h;o", "h's", "%x", strings.Repeat("a", 256)} {
		if ValidHost(h) {
			t.Errorf("reddedilmeli: %q", h)
		}
	}
}

func TestProtectedNames(t *testing.T) {
	for _, n := range []string{"postgres", "template0", "mysql", "server_control",
		"srvctl", "srvctl_panel", "serverctl_x", "information_schema"} {
		if !IsProtectedDatabase(n) {
			t.Errorf("korumalı sayılmalı: %q", n)
		}
	}
	if IsProtectedDatabase("app_db") || IsProtectedDatabase("srvctlx") {
		t.Fatal("sıradan adlar korumalı olmamalı")
	}
	for _, u := range []string{"root", "postgres", "mysql.session", "admin", "debian-sys-maint"} {
		if !IsProtectedUser(u) {
			t.Errorf("korumalı kullanıcı sayılmalı: %q", u)
		}
	}
	if IsProtectedUser("app_ro") {
		t.Fatal("sıradan kullanıcı korumalı olmamalı")
	}
}

// ---- şifre üretimi ----

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatalf("şifre: %v", err)
		}
		if len(pw) != PasswordLength {
			t.Fatalf("uzunluk %d", len(pw))
		}
		for _, c := range pw {
			if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", c) {
				t.Fatalf("charset dışı karakter: %q", pw)
			}
		}
		if seen[pw] {
			t.Fatal("şifre tekrarlandı")
		}
		seen[pw] = true
	}
}

// ---- SQL üretimi ----

func TestGrantSetSQLMatrix(t *testing.T) {
	pgRO := GrantSetSQL(KindPostgres, "app_ro", "", "app", LevelReadonly)
	if len(pgRO) < 5 || !strings.Contains(pgRO[0], `REVOKE ALL PRIVILEGES ON DATABASE "app" FROM "app_ro"`) {
		t.Fatalf("pg readonly: %v", pgRO)
	}
	joined := strings.Join(pgRO, " ")
	if strings.Contains(joined, "INSERT") || strings.Contains(joined, "GRANT ALL PRIVILEGES") {
		t.Fatal("readonly fazladan yetki vermemeli")
	}
	pgAll := GrantSetSQL(KindPostgres, "u", "", "d", LevelAll)
	if !strings.Contains(strings.Join(pgAll, " "), `GRANT ALL PRIVILEGES ON DATABASE "d"`) {
		t.Fatal("pg all eksik")
	}
	myRW := GrantSetSQL(KindMySQL, "u", "localhost", "d", LevelReadWrite)
	if !strings.Contains(myRW[0], "REVOKE ALL PRIVILEGES ON `d`.* FROM 'u'@'localhost'") ||
		!strings.Contains(myRW[1], "GRANT SELECT, INSERT, UPDATE, DELETE") {
		t.Fatalf("mysql readwrite: %v", myRW)
	}
	// Her seviye REVOKE ile başlamalı (desired-state yakınsaması).
	for _, lvl := range []string{LevelReadonly, LevelReadWrite, LevelAll} {
		if !strings.HasPrefix(GrantSetSQL(KindMySQL, "u", "%", "d", lvl)[0], "REVOKE") {
			t.Fatalf("%s REVOKE ile başlamalı", lvl)
		}
	}
}

func TestSQLBuilders(t *testing.T) {
	if got := DatabaseCreateSQL(KindPostgres, "app", "owner1"); got != `CREATE DATABASE "app" OWNER "owner1"` {
		t.Fatalf("pg create: %q", got)
	}
	if got := DatabaseCreateSQL(KindMySQL, "app", ""); got != "CREATE DATABASE IF NOT EXISTS `app`" {
		t.Fatalf("my create: %q", got)
	}
	if got := UserCreateSQL(KindPostgres, "u1", "", "pw123"); got != `CREATE ROLE "u1" LOGIN PASSWORD 'pw123'` {
		t.Fatalf("pg user: %q", got)
	}
	if got := UserCreateSQL(KindMySQL, "u1", "localhost", "pw123"); got != "CREATE USER 'u1'@'localhost' IDENTIFIED BY 'pw123'" {
		t.Fatalf("my user: %q", got)
	}
	if got := ExistsSQL(KindMySQL, "user", "u1", "10.0.0.5"); !strings.Contains(got, "'u1' AND Host = '10.0.0.5'") {
		t.Fatalf("my exists: %q", got)
	}
	if got := UserDeleteSQL(KindPostgres, "u1", ""); got != `DROP ROLE "u1"` {
		t.Fatalf("pg drop: %q", got)
	}
}

func TestBackupFileName(t *testing.T) {
	ts := time.Date(2026, 11, 2, 9, 5, 7, 0, time.UTC)
	got := BackupFileName("app", ts)
	if got != "app-20261102T090507Z.sql" || !ValidBackupName(got) {
		t.Fatalf("dosya adı: %q", got)
	}
	if ValidBackupName("../evil.sql") || ValidBackupName("a/b.sql") || ValidBackupName("-x.sql") {
		t.Fatal("yol içeren adlar reddedilmeli")
	}
}

// ---- runner mekanikleri (sahte istemcilerle) ----

func fakeBin(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func testRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{
		Mysql:     fakeBin(t, bin, "mysql", "echo '1'"),
		MysqlDump: fakeBin(t, bin, "mysqldump", "echo 'CREATE TABLE t (id int);'"),
		BackupDir: filepath.Join(base, "backups"),
		Timeout:   5 * time.Second,
	}, base
}

// Yedek: marker ilk satır + dump çıktısı + sha256/bytes doğru; restore
// marker'lı dosyayı kabul eder, markersız dosyayı TİPLİ reddeder.
func TestBackupRestoreLifecycle(t *testing.T) {
	r, _ := testRunner(t)
	ctx := context.Background()
	res, err := r.Backup(ctx, KindMySQL, "app")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if res["file"] == nil {
		t.Fatalf("sonuçta dosya yok: %v", res)
	}
	path := filepath.Join(r.backupKindDir(KindMySQL), res["file"].(string))
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if lines[0] != BackupMarker {
		t.Fatalf("ilk satır marker olmalı: %q", lines[0])
	}
	if !strings.Contains(string(data[len(BackupMarker):]), "CREATE TABLE") {
		t.Fatal("dump çıktısı dosyada olmalı")
	}
	if res["sha256"] == "" || res["bytes"] == 0 {
		t.Fatalf("sha256/bytes eksik: %v", res)
	}

	// restore: markersız dosya reddedilir.
	foreign := filepath.Join(r.backupKindDir(KindMySQL), "foreign-20261102T000000Z.sql")
	if err := os.WriteFile(foreign, []byte("DROP DATABASE x;"), 0o600); err != nil { //nolint:gosec // test path
		t.Fatal(err)
	}
	if _, err := r.Restore(ctx, KindMySQL, "app", "foreign-20261102T000000Z.sql"); err != ErrForeignBackup {
		t.Fatalf("markersız dosya ErrForeignBackup olmalı: %v", err)
	}
	// restore: yok dosya → ErrNotFound.
	if _, err := r.Restore(ctx, KindMySQL, "app", "nope-20261102T000000Z.sql"); err == nil || !strings.Contains(err.Error(), "bulunamadı") {
		t.Fatalf("yok dosya ErrNotFound olmalı: %v", err)
	}
}

// Yedek listesi: marker'sız/yabancı dosyalar listelenmez.
func TestBackupsListHidesForeign(t *testing.T) {
	r, _ := testRunner(t)
	ctx := context.Background()
	if _, err := r.Backup(ctx, KindMySQL, "app"); err != nil {
		t.Fatalf("backup: %v", err)
	}
	foreign := filepath.Join(r.backupKindDir(KindMySQL), "zzz-20261102T000000Z.sql")
	if err := os.WriteFile(foreign, []byte("evil"), 0o600); err != nil { //nolint:gosec // test path
		t.Fatal(err)
	}
	rows, err := r.BackupsList(ctx, KindMySQL, 500)
	if err != nil {
		t.Fatalf("liste: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("yalnız marker'lı dosya listelenmeli: %d", len(rows))
	}
	if rows[0].File == "zzz-20261102T000000Z.sql" {
		t.Fatal("yabancı dosya listede olmamalı")
	}
}

// Yedek sınırı: MaxBackupFiles aşıldığında tipli red.
func TestBackupFileLimit(t *testing.T) {
	r, _ := testRunner(t)
	dir := r.backupKindDir(KindMySQL)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBackupFiles; i++ {
		name := filepath.Join(dir, "app-20261102T0000"+string(rune('0'+i%10))+string(rune('a'+i/10))+"Z.sql")
		if err := os.WriteFile(name, []byte(BackupMarker+"\n-- x"), 0o600); err != nil { //nolint:gosec // test path
			t.Fatal(err)
		}
	}
	if _, err := r.Backup(context.Background(), KindMySQL, "app"); err == nil || !strings.Contains(err.Error(), "sınır") {
		t.Fatalf("dosya sınırı tipli red olmalı: %v", err)
	}
}

// Korumalı adlar: create/delete/backup TİPLİ reddeder (kriter 1 — panel
// veritabanı yönetim yüzeyine ULAŞAMAZ).
func TestProtectedRefusals(t *testing.T) {
	r, _ := testRunner(t)
	ctx := context.Background()
	for _, name := range []string{"postgres", "server_control", "srvctl_panel", "mysql"} {
		if _, err := r.DatabaseCreate(ctx, KindMySQL, name, ""); err == nil || !strings.Contains(err.Error(), "korumalı") {
			t.Errorf("create korumalı red olmalı: %q → %v", name, err)
		}
		if _, err := r.DatabaseDelete(ctx, KindMySQL, name); err == nil || !strings.Contains(err.Error(), "korumalı") {
			t.Errorf("delete korumalı red olmalı: %q → %v", name, err)
		}
		if _, err := r.Backup(ctx, KindMySQL, name); err == nil || !strings.Contains(err.Error(), "korumalı") {
			t.Errorf("backup korumalı red olmalı: %q → %v", name, err)
		}
	}
	// Not: "mysql.session" nokta içerdiğinden ad-RE katmanında reddedilir;
	// invariant: HANGİ katman olursa olsun red.
	for _, u := range []string{"root", "postgres", "mysql", "admin"} {
		if _, err := r.UserCreate(ctx, KindMySQL, u, "localhost"); err == nil || !strings.Contains(err.Error(), "korumalı") {
			t.Errorf("user create korumalı red olmalı: %q → %v", u, err)
		}
		if _, err := r.UserDelete(ctx, KindMySQL, u, "localhost"); err == nil || !strings.Contains(err.Error(), "korumalı") {
			t.Errorf("user delete korumalı red olmalı: %q → %v", u, err)
		}
	}
	if _, err := r.UserCreate(ctx, KindMySQL, "mysql.session", "localhost"); err == nil {
		t.Error("nokta içeren servis hesabı reddedilmeli")
	}
}

// Geçersiz girdiler istemciye HİÇ ulaşmaz (tahmin yok).
func TestInvalidInputsRefusedBeforeExec(t *testing.T) {
	r, _ := testRunner(t)
	ctx := context.Background()
	if _, err := r.DatabaseCreate(ctx, "oracle", "app", ""); err != ErrInvalidKind {
		t.Fatalf("kind: %v", err)
	}
	if _, err := r.DatabaseCreate(ctx, KindMySQL, "App;", ""); err == nil {
		t.Fatal("RE dışı ad reddedilmeli")
	}
	if _, err := r.UserCreate(ctx, KindMySQL, "u1", "h;drop"); err == nil {
		t.Fatal("RE dışı host reddedilmeli")
	}
	if _, err := r.GrantSet(ctx, KindMySQL, "u1", "localhost", "app", "superuser"); err == nil {
		t.Fatal("matris dışı seviye reddedilmeli")
	}
	if _, err := r.Restore(ctx, KindMySQL, "app", "../../etc/passwd"); err == nil {
		t.Fatal("yol içeren yedek adı reddedilmeli")
	}
}

// hasBackupMarker: kısa/bozuk dosyada false, marker dosyada true.
func TestHasBackupMarker(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.sql")
	if err := os.WriteFile(ok, []byte(BackupMarker+"\nSELECT 1;"), 0o600); err != nil { //nolint:gosec // test path
		t.Fatal(err)
	}
	if marked, err := hasBackupMarker(ok); err != nil || !marked {
		t.Fatalf("marker bekleniyordu: %v %v", marked, err)
	}
	empty := filepath.Join(dir, "empty.sql")
	if err := os.WriteFile(empty, []byte(""), 0o600); err != nil { //nolint:gosec // test path
		t.Fatal(err)
	}
	if marked, err := hasBackupMarker(empty); err != nil || marked {
		t.Fatalf("boş dosya marker'sız olmalı: %v %v", marked, err)
	}
}

// Kullanıcı listesi şifre materyali TAŞIMAZ (kriter 2 — redaksiyon yüzeyi).
func TestUsersListNoPasswordColumns(t *testing.T) {
	if strings.Contains(ListSQL(KindPostgres, "user"), "passwd") ||
		strings.Contains(ListSQL(KindMySQL, "user"), "auth_string") ||
		strings.Contains(ListSQL(KindMySQL, "user"), "authentication_string") {
		t.Fatal("kullanıcı listesi şifre kolonu seçmemeli")
	}
}

// CI-34 (gerçek-host kanıtından): MySQL/MariaDB'de hiç grant verilmemiş
// kullanıcı için REVOKE ERROR 1141 üretir — desired-state'in İLK
// uygulaması bozulur. Runner, db-düzeyi yetki YOKSA REVOKE'u atlar.
// Sahte mysql istemcisi argv'larını kaydeder; probe cevabına göre
// yürütülen SQL doğrulanır.
func TestGrantSetMySQLConditionalRevoke(t *testing.T) {
	for _, tc := range []struct {
		name       string
		probeOut   string
		wantRevoke bool
	}{
		{"grants-exist", "1", true},
		{"no-grants", "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			logFile := filepath.Join(base, "argv.log")
			// Sahte istemci: yalnız SCHEMA_PRIVILEGES probe'una tc.probeOut
			// döndürür; diğer varlık sorguları ("1") — kullanıcı/DB var.
			script := ("#!/bin/sh\n" +
				"printf '%s\\n' \"$*\" >> " + logFile + "\n" +
				"case \"$*\" in *SCHEMA_PRIVILEGES*) echo " + tc.probeOut + " ;; *) echo 1 ;; esac\n")
			r := &Runner{
				Mysql:     fakeBin(t, base, "mysql", script),
				BackupDir: filepath.Join(base, "backups"),
				Timeout:   5 * time.Second,
			}
			// Veritabanı ve kullanıcı gerçek istemcide "var" sayılır
			// (sahte istemci her varlık sorgusuna 1 döndürür).
			if _, err := r.GrantSet(context.Background(), KindMySQL, "app_ro", "localhost", "app_db", LevelReadonly); err != nil {
				t.Fatalf("grant set: %v", err)
			}
			log, err := os.ReadFile(logFile) //nolint:gosec // test path
			if err != nil {
				t.Fatal(err)
			}
			executed := string(log)
			if !strings.Contains(executed, "GRANT SELECT") {
				t.Fatalf("GRANT yürütülmeli: %s", executed)
			}
			hasRevoke := strings.Contains(executed, "REVOKE ALL PRIVILEGES")
			if hasRevoke != tc.wantRevoke {
				t.Fatalf("REVOKE varlığı %v bekleniyordu; log: %s", tc.wantRevoke, executed)
			}
			// Probe, kaçışlı grantee biçimiyle sorulmalı.
			if !strings.Contains(executed, `'''app_ro'@'localhost'''`) {
				t.Fatalf("probe grantee kaçışı hatalı: %s", executed)
			}
		})
	}
}
