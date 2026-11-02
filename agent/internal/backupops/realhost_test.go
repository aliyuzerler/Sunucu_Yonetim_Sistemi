//go:build linux

// Faz 14 gerçek-host kanıt testi: runner'da GERÇEK restic ile tam backup
// yaşam döngüsü (repo init → backup → snapshots → verify → izole restore).
// EXPECTED_BACKUP=1 gate'i CI-17 dersidir: env set edildiğinde gerçek kanıt
// ZORUNLUDUR — sessiz atlanamaz. CI agent-backup job'ında koşar.
//
// Yollar: repo/parola/restore kökleri TEST alt dizinlerine yönlendirilir
// (üretim konumlarına dokunulmaz; runner disposable'dır — yalnız citest
// önekli dizinler kullanılır).
package backupops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireBackupGate(t *testing.T) {
	t.Helper()
	if os.Getenv("EXPECTED_BACKUP") != "1" {
		t.Skip("EXPECTED_BACKUP=1 gerekir (CI agent-backup job'ı; yerelde atlanır)")
	}
	if _, err := exec.LookPath("restic"); err != nil {
		t.Fatalf("EXPECTED_BACKUP=1 ama restic yok — sessiz atlama YASAK (CI-17): %v", err)
	}
}

func TestRealResticLifecycle(t *testing.T) {
	requireBackupGate(t)
	dir := t.TempDir()
	repo := filepath.Join(dir, "citest-restic-repo")
	pass := filepath.Join(dir, "citest-restic-pass")
	restoreRoot := filepath.Join(dir, "citest-restore")
	if err := os.WriteFile(pass, []byte("citest-fake-password-material-0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Yedek KAYNAĞI gerçek bir yönetilen dizin olsun: /etc/server-control
	// üretim konumudur; runner'da citest kaynak dizini kullanılır.
	src := filepath.Join(dir, "citest-source")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc-backup.txt"), []byte("citest backup source\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		RepoDir:      repo,
		PasswordFile: pass,
		RestoreRoot:  restoreRoot,
		Sources:      []string{src},
		Timeout:      120 * time.Second,
	}
	ctx := context.Background()

	// 1) Repo init — idempotent.
	res, err := r.RepoInit(ctx)
	if err != nil {
		t.Fatalf("RepoInit: %v", err)
	}
	if res["initialized"] != true {
		t.Fatalf("initialized bekleniyordu: %v", res)
	}
	res2, err := r.RepoInit(ctx)
	if err != nil || res2["already"] != true {
		t.Fatalf("ikinci init already=true olmalı: %v %v", res2, err)
	}

	// 2) Backup — gerçek snapshot + inline verify.
	br, err := r.Backup(ctx, RunPayload{Paths: []string{src}})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if br.SnapshotID == "" || br.SizeBytes <= 0 {
		t.Fatalf("snapshot özeti: %+v", br)
	}
	if br.VerifyState != VerifyVerified {
		t.Fatalf("gerçek check verified dönmeli: %+v", br)
	}

	// 3) Snapshot list — snapshot listede.
	rows, err := r.SnapshotList(ctx, SnapshotListPayload{Limit: 10})
	if err != nil || len(rows) == 0 {
		t.Fatalf("SnapshotList: %v %+v", err, rows)
	}
	found := false
	for _, row := range rows {
		if strings.HasPrefix(row.ID, br.SnapshotID) || strings.HasPrefix(br.SnapshotID, row.ShortID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot listede yok: %+v vs %s", rows, br.SnapshotID)
	}

	// 4) Verify — gerçek read-data-subset doğrulaması.
	vr, err := r.Verify(ctx, VerifyPayload{BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vr.VerifyState != VerifyVerified {
		t.Fatalf("verify state: %+v", vr)
	}

	// 5) Restore — İZOLE hedefe gerçek geri yükleme + içerik kanıtı.
	rr, err := r.Restore(ctx, RestorePayload{
		BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111",
		Snapshot: br.SnapshotID,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	root, rerr := filepath.Abs(restoreRoot)
	if rerr != nil {
		t.Fatal(rerr)
	}
	tgt, terr := filepath.Abs(rr.Target)
	if terr != nil {
		t.Fatal(terr)
	}
	if !strings.HasPrefix(tgt, root+string(filepath.Separator)) {
		t.Fatalf("restore hedefi izole kök dışında: %s", rr.Target)
	}
	// restic restore, snapshot'taki mutlak yolu hedef altında yeniden
	// oluşturur (baştaki '/' atılır): <target>/tmp/<TempDir>/citest-source/...
	// Beklenti restic'in GERÇEK düzenine göre yazılır (bağımsız test
	// bulgusu: elle doğrulanmış restic 0.18 davranışı).
	rel := strings.TrimPrefix(src, string(filepath.Separator))
	data, derr := os.ReadFile(filepath.Join(rr.Target, rel, "etc-backup.txt"))
	if derr != nil {
		t.Fatalf("geri yüklenen dosya: %v", derr)
	}
	if !strings.Contains(string(data), "citest backup source") {
		t.Fatalf("geri yüklenen içerik uyuşmuyor: %q", string(data))
	}
}
