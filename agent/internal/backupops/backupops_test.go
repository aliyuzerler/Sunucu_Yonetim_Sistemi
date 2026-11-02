//go:build linux

package backupops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRestic writes a bash script that emulates the restic CLI surface we
// use: init / backup --json / snapshots --json / check / restore. State is
// kept in STATE-<name> files so tests can script failures (CI-49 lesson:
// no realistic secrets in source; fake ids are random-shaped hex only).
func fakeRestic(t *testing.T, dir, behavior string) string {
	t.Helper()
	path := filepath.Join(dir, "restic")
	script := `#!/bin/bash
# Fake restic for hermetic tests. Behavior scripted by the test.
STATE="` + filepath.Join(dir, "state") + `"
REPO=""
ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --repo) REPO="$2"; shift 2 ;;
    --password-file) shift 2 ;;
    *) ARGS+=("$1"); shift ;;
  esac
done
CMD="${ARGS[0]}"
case "$CMD" in
  init)
    if [ -f "$STATE/repoinit" ]; then echo "already initialized"; exit 1; fi
    touch "$STATE/repoinit"
    echo "created restic repo"
    ;;
  backup)
    if [ ! -f "$STATE/repoinit" ]; then echo "repo missing" >&2; exit 10; fi
    echo '{"message_type":"summary","snapshot_id":"abcd1234ef567890","total_bytes_processed":123456,"total_files_processed":42}'
    ;;
  snapshots)
    if [ ! -f "$STATE/repoinit" ]; then echo "repo missing" >&2; exit 10; fi
    if [ -f "$STATE/snapfail" ]; then echo "snapshots broken" >&2; exit 11; fi
    echo '[{"id":"abcd1234ef5678900000000000000000000000000000000000000000000000","short_id":"abcd1234","time":"2026-11-03T10:00:00Z","hostname":"server-control","paths":["/etc/server-control"],"tags":[]}]'
    ;;
  check)
    if [ -f "$STATE/checkfail" ]; then echo "check broken" >&2; exit 12; fi
    echo "no errors"
    ;;
  restore)
    if [ ! -f "$STATE/repoinit" ]; then echo "repo missing" >&2; exit 10; fi
    # Target follows --target <dir>
    for i in "${!ARGS[@]}"; do
      if [ "${ARGS[$i]}" = "--target" ]; then mkdir -p "${ARGS[$((i+1))]}"; fi
    done
    echo '{"message_type":"summary","total_files":7,"total_bytes":4096}'
    ;;
  *) echo "unknown cmd $CMD" >&2; exit 99 ;;
esac
`
	// Behavior tweaks applied by tests through the STATE dir (pre-created
	// before the runner executes).
	_ = behavior
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	pass := filepath.Join(dir, "passfile")
	if err := os.WriteFile(pass, []byte("fake-password-material"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Runner{
		Restic:       fakeRestic(t, dir, ""),
		RepoDir:      repo,
		PasswordFile: pass,
		RestoreRoot:  filepath.Join(dir, "restore"),
		Sources:      []string{"/etc/server-control", "/var/vmail"},
		now:          func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}, state
}

func TestRepoInitIdempotent(t *testing.T) {
	r, state := newRunner(t)
	ctx := context.Background()
	res, err := r.RepoInit(ctx)
	if err != nil {
		t.Fatalf("RepoInit: %v", err)
	}
	if res["already"] != false {
		t.Fatalf("ilk init already=false olmalı: %v", res)
	}
	// Fake: repoinit varken snapshots başarılı → ikinci init already=true.
	res2, err := r.RepoInit(ctx)
	if err != nil {
		t.Fatalf("ikinci RepoInit: %v", err)
	}
	if res2["already"] != true {
		t.Fatalf("ikinci init already=true olmalı: %v", res2)
	}
	_ = state
}

func TestBackupAndHonestVerifyState(t *testing.T) {
	r, state := newRunner(t)
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := r.Backup(ctx, RunPayload{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if res.SnapshotID != "abcd1234ef567890" || res.SizeBytes != 123456 || res.Files != 42 {
		t.Fatalf("özet parse hatası: %+v", res)
	}
	if res.VerifyState != VerifyVerified {
		t.Fatalf("verify_state verified bekleniyordu: %s", res.VerifyState)
	}
	// check bozulunca dürüst failed — snapshot yine de sonuçta.
	if err := os.WriteFile(filepath.Join(state, "checkfail"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res2, err := r.Backup(ctx, RunPayload{})
	if err != nil {
		t.Fatalf("check-fail Backup: %v", err)
	}
	if res2.VerifyState != VerifyFailed {
		t.Fatalf("bozuk check'te verify_state failed olmalı: %s", res2.VerifyState)
	}
}

func TestBackupRejectsPathsOutsideSources(t *testing.T) {
	r, _ := newRunner(t)
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Backup(ctx, RunPayload{Paths: []string{"/etc/shadow"}}); err == nil {
		t.Fatal("kaynak listesi dışı yol reddedilmeli")
	}
	if _, err := r.Backup(ctx, RunPayload{Paths: []string{"/etc/server-control"}}); err != nil {
		t.Fatalf("izinli yol kabul edilmeli: %v", err)
	}
}

func TestSnapshotList(t *testing.T) {
	r, _ := newRunner(t)
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := r.SnapshotList(ctx, SnapshotListPayload{Limit: 10})
	if err != nil {
		t.Fatalf("SnapshotList: %v", err)
	}
	if len(rows) != 1 || rows[0].ShortID != "abcd1234" {
		t.Fatalf("satır parse hatası: %+v", rows)
	}
	// Repo yoksa tipli precondition.
	r2, _ := newRunner(t)
	if _, err := r2.SnapshotList(ctx, SnapshotListPayload{}); err == nil {
		t.Fatal("repo olmadan liste tipli hata vermeli")
	}
}

func TestVerifyStates(t *testing.T) {
	r, state := newRunner(t)
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err != nil {
		t.Fatal(err)
	}
	vr, err := r.Verify(ctx, VerifyPayload{BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vr.VerifyState != VerifyVerified {
		t.Fatalf("verified bekleniyordu: %s", vr.VerifyState)
	}
	if err := os.WriteFile(filepath.Join(state, "checkfail"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	vr2, err := r.Verify(ctx, VerifyPayload{BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"})
	if err != nil {
		t.Fatalf("bozuk check hata döndürmemeli (dürüst state): %v", err)
	}
	if vr2.VerifyState != VerifyFailed {
		t.Fatalf("failed bekleniyordu: %s", vr2.VerifyState)
	}
	// Geçersiz backup_id tipli red.
	if _, err := r.Verify(ctx, VerifyPayload{BackupID: "zort"}); err == nil {
		t.Fatal("geçersiz backup_id reddedilmeli")
	}
}

func TestRestoreIsolatedTarget(t *testing.T) {
	r, _ := newRunner(t)
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := r.Restore(ctx, RestorePayload{
		BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111",
		Snapshot: "abcd1234",
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	// Hedef yalnız izole kök altında olabilir.
	root, _ := filepath.Abs(r.RestoreRoot)
	tgt, _ := filepath.Abs(res.Target)
	if !strings.HasPrefix(tgt, root+string(filepath.Separator)) {
		t.Fatalf("hedef izole kök dışında: %s", res.Target)
	}
	if res.Files != 7 || res.Bytes != 4096 {
		t.Fatalf("restore özeti: %+v", res)
	}
	if st, err := os.Stat(res.Target); err != nil || !st.IsDir() {
		t.Fatalf("restore hedefi yaratılmalıydı: %v", err)
	}
	// Geçersiz snapshot tipli red.
	if _, err := r.Restore(ctx, RestorePayload{BackupID: "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111", Snapshot: "ZZZZ"}); err == nil {
		t.Fatal("geçersiz snapshot reddedilmeli")
	}
}

func TestKeySeparationEnforced(t *testing.T) {
	// Parola dosyası repo İÇİNDE → tüm işlemler tipli red.
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	pass := filepath.Join(repo, "restic-password")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pass, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		Restic:       fakeRestic(t, dir, ""),
		RepoDir:      repo,
		PasswordFile: pass,
		RestoreRoot:  filepath.Join(dir, "restore"),
		Sources:      []string{"/etc/server-control"},
	}
	if err := ValidateKeySeparation(repo, pass); err == nil {
		t.Fatal("repo içi parola dosyası reddedilmeli")
	}
	ctx := context.Background()
	if _, err := r.RepoInit(ctx); err == nil {
		t.Fatal("RepoInit key separation ihlalinde rededeilmeli")
	}
	if _, err := r.Backup(ctx, RunPayload{}); err == nil {
		t.Fatal("Backup key separation ihlalinde reddedilmeli")
	}
}

func TestPasswordFileMissingTyped(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{
		Restic:       fakeRestic(t, dir, ""),
		RepoDir:      filepath.Join(dir, "repo"),
		PasswordFile: filepath.Join(dir, "yok"),
		RestoreRoot:  filepath.Join(dir, "restore"),
		Sources:      []string{"/etc/server-control"},
	}
	if _, err := r.RepoInit(context.Background()); err != ErrRepoPassword {
		t.Fatalf("ErrRepoPassword bekleniyordu: %v", err)
	}
}

func TestResticMissingTyped(t *testing.T) {
	dir := t.TempDir()
	pass := filepath.Join(dir, "pass")
	if err := os.WriteFile(pass, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		Restic:       filepath.Join(dir, "yok-restic"),
		RepoDir:      filepath.Join(dir, "repo"),
		PasswordFile: pass,
		RestoreRoot:  filepath.Join(dir, "restore"),
	}
	if _, err := r.RepoInit(context.Background()); err != ErrUnavailable {
		t.Fatalf("ErrUnavailable bekleniyordu: %v", err)
	}
}

func TestValidators(t *testing.T) {
	uid := "0b7fd4d2-1234-4c2b-9a53-2b6f8f8a1111"
	if err := ValidateRun(RunPayload{Paths: []string{"goreceli"}}); err == nil {
		t.Fatal("göreceli yol reddedilmeli")
	}
	if err := ValidateRun(RunPayload{Paths: []string{"/etc/../shadow"}}); err == nil {
		t.Fatal("traversal reddedilmeli")
	}
	if err := ValidateSnapshotList(SnapshotListPayload{Limit: 101}); err == nil {
		t.Fatal("limit 100 aşımı reddedilmeli")
	}
	if err := ValidateVerify(VerifyPayload{BackupID: "x"}); err == nil {
		t.Fatal("bozuk uuid reddedilmeli")
	}
	if err := ValidateRestore(RestorePayload{BackupID: uid, Snapshot: "abcd1234"}); err != nil {
		t.Fatalf("geçerli restore reddedildi: %v", err)
	}
	// Sonuç şeması gizli taşımaz: BackupResult JSON'unda password yok.
	b, _ := json.Marshal(BackupResult{SnapshotID: "abcd1234"})
	if strings.Contains(string(b), "password") {
		t.Fatal("sonuç şemasında parola alanı olmamalı")
	}
}
