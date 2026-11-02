//go:build linux

// Faz 10 gerçek-host kanıt testi: runner'da GERÇEK PostgreSQL ve MariaDB
// ile DB yaşam döngüsü (create → grant → backup → restore → delete) ve
// GERÇEK dosya yöneticisi (docroot scope + upload/download/chmod/chown).
// EXPECTED_DB=1 / EXPECTED_FS=1 gate'i CI-17 dersidir: env set edildiğinde
// gerçek kanıt ZORUNLUDUR — sessiz atlanamaz. CI agent-dbfs job'ında koşar.
package dbops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireGate(t *testing.T) {
	t.Helper()
	if os.Getenv("EXPECTED_DB") != "1" {
		t.Skip("EXPECTED_DB=1 gerekir (CI agent-dbfs job'ı; yerelde atlanır)")
	}
	for _, b := range []string{"psql", "pg_dump", "mysql", "mysqldump", "runuser", "systemctl"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Fatalf("EXPECTED_DB=1 ama %s yok — sessiz atlama YASAK (CI-17): %v", b, err)
		}
	}
}

// TestRealDBLifecycle: gerçek PostgreSQL + MariaDB üzerinde tam yaşam
// döngüsü. Yalnız `citest-` önekli adlar kullanılır; sistem/panel adlarına
// dokunulmaz.
func TestRealDBLifecycle(t *testing.T) {
	requireGate(t)
	r := &Runner{Timeout: 120 * time.Second}
	ctx := context.Background()

	// 1) Instance detection: her iki sunucu da kurulu olmalı.
	insts, err := r.InstancesList(ctx)
	if err != nil {
		t.Fatalf("instances: %v", err)
	}
	kinds := map[string]bool{}
	for _, i := range insts {
		kinds[i.Kind] = true
		if i.Version == "" {
			t.Fatalf("%s sürüm raporlanmalı: %+v", i.Kind, i)
		}
	}
	if !kinds[KindPostgres] || !kinds[KindMySQL] {
		t.Fatalf("her iki instance da bulunmalı: %+v", insts)
	}

	// 2) Protected refusal — gerçek sunucuda bile tipli red.
	if _, err := r.DatabaseDelete(ctx, KindPostgres, "server_control"); err == nil ||
		!strings.Contains(err.Error(), "korumalı") {
		t.Fatalf("panel DB koruması gerçek sunucuda da geçerli olmalı: %v", err)
	}

	for _, kind := range []string{KindPostgres, KindMySQL} {
		db := "citest_faz10"
		user := "citest_ro"

		// 3) database create (idempotent: ikinci çağrı no-op).
		res, err := r.DatabaseCreate(ctx, kind, db, "")
		if err != nil {
			t.Fatalf("%s create: %v", kind, err)
		}
		if res["created"] != true {
			t.Fatalf("%s ilk create true olmalı: %v", kind, res)
		}
		res2, err := r.DatabaseCreate(ctx, kind, db, "")
		if err != nil || res2["already_exists"] != true {
			t.Fatalf("%s create idempotent: %v %v", kind, err, res2)
		}

		// 4) user create → bir-kerelik şifre.
		uc, err := r.UserCreate(ctx, kind, user, "localhost")
		if err != nil {
			t.Fatalf("%s user create: %v", kind, err)
		}
		if uc.Credentials == nil || len(uc.Credentials.Password) != PasswordLength {
			t.Fatalf("%s user create şifre: %+v", kind, uc)
		}
		if _, err := r.UserCreate(ctx, kind, user, "localhost"); err != nil {
			t.Fatalf("%s user create replay: %v", kind, err)
		}

		// 5) grant readonly.
		if _, err := r.GrantSet(ctx, kind, user, "localhost", db, LevelReadonly); err != nil {
			t.Fatalf("%s grant: %v", kind, err)
		}

		// 6) backup → dosya + marker + sha256.
		bres, err := r.Backup(ctx, kind, db)
		if err != nil {
			t.Fatalf("%s backup: %v", kind, err)
		}
		file, _ := bres["file"].(string)
		if file == "" || bres["sha256"] == "" || bres["bytes"].(int64) <= 0 {
			t.Fatalf("%s backup sonucu: %v", kind, bres)
		}

		// 7) restore (kendi yedeğimiz).
		if _, err := r.Restore(ctx, kind, db, file); err != nil {
			t.Fatalf("%s restore: %v", kind, err)
		}

		// 8) user delete + database delete (temizlik + yaşam döngüsü kanıtı).
		if _, err := r.UserDelete(ctx, kind, user, "localhost"); err != nil {
			t.Fatalf("%s user delete: %v", kind, err)
		}
		if _, err := r.DatabaseDelete(ctx, kind, db); err != nil {
			t.Fatalf("%s database delete: %v", kind, err)
		}
	}
}

// TestRealDBBackupContent: pg_dump çıktısı gerçek SQL içerir ve marker
// ilk satırdır.
func TestRealDBBackupContent(t *testing.T) {
	requireGate(t)
	r := &Runner{Timeout: 120 * time.Second}
	ctx := context.Background()
	db := "citest_faz10b"
	if _, err := r.DatabaseCreate(ctx, KindPostgres, db, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = r.DatabaseDelete(context.Background(), KindPostgres, db) })
	res, err := r.Backup(ctx, KindPostgres, db)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	path := filepath.Join(r.backupKindDir(KindPostgres), res["file"].(string))
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), BackupMarker) {
		t.Fatal("marker ilk satır olmalı")
	}
	if !strings.Contains(string(data), "PostgreSQL database dump") {
		t.Fatalf("gerçek pg_dump çıktısı beklenir (ilk 200 bayt): %.200s", data)
	}
	_ = os.Remove(path)
}
