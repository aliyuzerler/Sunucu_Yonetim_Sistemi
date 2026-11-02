//go:build linux

package backupops

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Default node-managed locations. The repo and the password file live in
// DIFFERENT root trees by design (kriter 3); both are injectable for
// hermetic tests.
const (
	DefaultRepoDir      = "/var/backups/server-control/restic"
	DefaultPasswordFile = "/etc/server-control/backup/restic-password"
	DefaultRestoreRoot  = "/var/backups/server-control/restore"
	DefaultSource       = "/etc/server-control"
)

// Runner executes restic operations with fixed argv. Tool path, repo
// location, password file and sources are injectable for hermetic tests; the
// zero value looks restic up on PATH and uses the documented defaults.
type Runner struct {
	Restic       string   // restic binary ("" → LookPath)
	RepoDir      string   // node-managed repository (default DefaultRepoDir)
	PasswordFile string   // RESTIC_PASSWORD_FILE target (default DefaultPasswordFile)
	RestoreRoot  string   // isolated restore root (default DefaultRestoreRoot)
	Sources      []string // allowlisted backup sources (default [/etc/server-control])
	Timeout      time.Duration
	// now is overridable for deterministic restore dir names.
	now func() time.Time
}

// SnapshotRow is one listed restic snapshot.
type SnapshotRow struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"short_id"`
	Time     string   `json:"time"`
	Hostname string   `json:"hostname"`
	Paths    []string `json:"paths"`
	Tags     []string `json:"tags"`
}

// BackupResult is the typed outcome of backup.run. It contains NO password
// material and no repository keys — only identity + size + verify state.
type BackupResult struct {
	SnapshotID   string `json:"snapshot_id"`
	SizeBytes    int64  `json:"size_bytes"`
	Files        int64  `json:"files"`
	VerifyState  string `json:"verify_state"`
	VerifyDetail string `json:"verify_detail"`
}

// VerifyResult is the typed outcome of backup.verify.
type VerifyResult struct {
	VerifyState  string `json:"verify_state"`
	VerifyDetail string `json:"verify_detail"`
}

// RestoreResult is the typed outcome of backup.restore. Target is the
// ISOLATED directory chosen by the node (never payload-supplied).
type RestoreResult struct {
	BackupID string `json:"backup_id"`
	Target   string `json:"target"`
	Files    int64  `json:"files"`
	Bytes    int64  `json:"bytes"`
	Detail   string `json:"detail"`
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 4 * time.Minute
}

func (r *Runner) repo() string {
	if r.RepoDir != "" {
		return r.RepoDir
	}
	return DefaultRepoDir
}

func (r *Runner) passwordFile() string {
	if r.PasswordFile != "" {
		return r.PasswordFile
	}
	return DefaultPasswordFile
}

func (r *Runner) restoreRoot() string {
	if r.RestoreRoot != "" {
		return r.RestoreRoot
	}
	return DefaultRestoreRoot
}

func (r *Runner) sources() []string {
	if len(r.Sources) > 0 {
		return r.Sources
	}
	return []string{DefaultSource}
}

func (r *Runner) nowFunc() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// resolve looks a tool up (injectable path wins).
func resolve(injected, name string) (string, bool) {
	if injected != "" {
		return injected, true
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// checkPrereqs validates the structural invariants every op depends on:
// restic present, password file readable, key separation intact.
func (r *Runner) checkPrereqs() (string, error) {
	bin, ok := resolve(r.Restic, "restic")
	if !ok {
		return "", ErrUnavailable
	}
	// Injected paths may point at deleted binaries — verify existence.
	if st, serr := os.Stat(bin); serr != nil || st.IsDir() {
		return "", ErrUnavailable
	}
	if err := ValidateKeySeparation(r.repo(), r.passwordFile()); err != nil {
		return "", err
	}
	if st, err := os.Stat(r.passwordFile()); err != nil || st.IsDir() {
		return "", ErrRepoPassword
	}
	return bin, nil
}

// ValidateKeySeparation enforces kriter 3 at runtime: the password file must
// never live inside the repository directory (or any subdirectory).
func ValidateKeySeparation(repoDir, passwordFile string) error {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return fmt.Errorf("%w: repo yolu: %v", ErrKeySeparation, err)
	}
	absPass, err := filepath.Abs(passwordFile)
	if err != nil {
		return fmt.Errorf("%w: parola yolu: %v", ErrKeySeparation, err)
	}
	if absRepo == absPass {
		return fmt.Errorf("%w: parola dosyası repo dizininin kendisi", ErrKeySeparation)
	}
	rel, relErr := filepath.Rel(absRepo, absPass)
	if relErr != nil {
		return nil // different roots → not contained
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s", ErrKeySeparation, passwordFile)
	}
	return nil
}

// baseArgv returns the common restic prefix (repo + password env only).
func (r *Runner) baseArgv(bin string) []string {
	return []string{bin, "--repo", r.repo(), "--password-file", r.passwordFile()}
}

// waitTimeout bounds a single restic invocation.
func (r *Runner) runArgv(ctx context.Context, argv ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	c := exec.CommandContext(cctx, argv[0], argv[1:]...) //nolint:gosec // fixed binary + argv, no shell
	c.Env = append(os.Environ(), "RESTIC_PASSWORD_FILE="+r.passwordFile())
	var stdout, stderr strings.Builder
	// stdout can carry NDJSON (bounded); stderr detail stays on the node for
	// log safety — errors carry a short program context only.
	c.Stdout = &boundedWriter{w: &stdout, limit: MaxOutputBytes}
	c.Stderr = &boundedWriter{w: &stderr, limit: 64 << 10}
	if err := c.Run(); err != nil {
		if ctxErr := cctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("restic zaman aşımı: %w", ctxErr)
		}
		return nil, fmt.Errorf("restic başarısız: %w", err)
	}
	return []byte(stdout.String()), nil
}

// boundedWriter caps captured output (log-safety + memory bound).
type boundedWriter struct {
	w     *strings.Builder
	limit int
	n     int
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if b.n >= b.limit {
		return len(p), nil // swallow beyond the cap
	}
	room := b.limit - b.n
	if len(p) > room {
		b.w.Write(p[:room])
		b.n = b.limit
		return len(p), nil
	}
	b.w.Write(p)
	b.n += len(p)
	return len(p), nil
}

// RepoInit initializes the restic repository (idempotent desired state: an
// already-initialized repo succeeds with already=true, no error).
func (r *Runner) RepoInit(ctx context.Context) (map[string]any, error) {
	bin, err := r.checkPrereqs()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(r.repo(), 0o700); err != nil {
		return nil, fmt.Errorf("%w: repo dizini: %v", ErrRepoInitFailed, err)
	}
	// Idempotency probe: restic refuses to init an existing repo; snapshots
	// on an initialized repo succeed (any restic op proves initialization).
	if _, err := r.runArgv(ctx, append(r.baseArgv(bin), "snapshots", "--json")...); err == nil {
		return map[string]any{"initialized": true, "already": true}, nil
	}
	argv := append(r.baseArgv(bin), "init")
	if _, err := r.runArgv(ctx, argv...); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRepoInitFailed, err)
	}
	return map[string]any{"initialized": true, "already": false}, nil
}

// Backup runs one restic backup over the allowlisted paths and verifies the
// repository integrity afterwards (metadata check) — verify_state is
// reported honestly (kriter 2).
func (r *Runner) Backup(ctx context.Context, payload RunPayload) (BackupResult, error) {
	bin, err := r.checkPrereqs()
	if err != nil {
		return BackupResult{}, err
	}
	paths := payload.Paths
	if len(paths) == 0 {
		paths = r.sources()
	}
	if err := ValidateRun(RunPayload{Paths: paths}); err != nil {
		return BackupResult{}, err
	}
	if err := pathsWithinSources(paths, r.sources()); err != nil {
		return BackupResult{}, err
	}
	argv := append(r.baseArgv(bin), "backup", "--json", "--host", "server-control")
	argv = append(argv, paths...)
	out, err := r.runArgv(ctx, argv...)
	if err != nil {
		return BackupResult{}, fmt.Errorf("%w: %v", ErrBackupFailed, err)
	}
	res := parseBackupSummary(out)
	// Integrity check (metadata level) right after the snapshot: a backup is
	// not complete until verification is recorded (kriter 2).
	checkOut, cerr := r.runArgv(ctx, append(r.baseArgv(bin), "check")...)
	if cerr != nil {
		res.VerifyState = VerifyFailed
		res.VerifyDetail = "restic check başarısız"
		return res, nil // snapshot exists; state reported honestly
	}
	_ = checkOut
	res.VerifyState = VerifyVerified
	res.VerifyDetail = "restic check (metadata) tamamlandı"
	return res, nil
}

// parseBackupSummary extracts the last summary line of restic backup --json.
func parseBackupSummary(out []byte) BackupResult {
	res := BackupResult{VerifyState: VerifyUnverified}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			MessageType         string `json:"message_type"`
			SnapshotID          string `json:"snapshot_id"`
			TotalBytesProcessed int64  `json:"total_bytes_processed"`
			TotalFilesProcessed int64  `json:"total_files_processed"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.MessageType == "summary" {
			res.SnapshotID = line.SnapshotID
			res.SizeBytes = line.TotalBytesProcessed
			res.Files = line.TotalFilesProcessed
		}
	}
	return res
}

// SnapshotList lists snapshots (bounded).
func (r *Runner) SnapshotList(ctx context.Context, payload SnapshotListPayload) ([]SnapshotRow, error) {
	bin, err := r.checkPrereqs()
	if err != nil {
		return nil, err
	}
	if err := ValidateSnapshotList(payload); err != nil {
		return nil, err
	}
	out, err := r.runArgv(ctx, append(r.baseArgv(bin), "snapshots", "--json")...)
	if err != nil {
		// Missing repo is a typed precondition, not a generic failure.
		return nil, fmt.Errorf("%w: %v", ErrRepoMissing, err)
	}
	var rows []SnapshotRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("%w: snapshots çıktısı: %v", ErrBackupFailed, err)
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = MaxSnapshotLimit
	}
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows, nil
}

// Verify runs a deeper integrity check (partial data read subset) and
// reports the state honestly.
func (r *Runner) Verify(ctx context.Context, payload VerifyPayload) (VerifyResult, error) {
	bin, err := r.checkPrereqs()
	if err != nil {
		return VerifyResult{}, err
	}
	if err := ValidateVerify(payload); err != nil {
		return VerifyResult{}, err
	}
	// `restic check` validates the repository structure; the data subset
	// performs a real partial read (documented 10%).
	_, err = r.runArgv(ctx, append(r.baseArgv(bin), "check", "--read-data-subset=10%")...)
	if err != nil {
		return VerifyResult{VerifyState: VerifyFailed, VerifyDetail: "restic check --read-data-subset=10% başarısız"}, nil
	}
	return VerifyResult{VerifyState: VerifyVerified, VerifyDetail: "restic check --read-data-subset=10% tamamlandı"}, nil
}

// Restore restores one snapshot into a FRESH isolated directory under the
// managed restore root (kriter 2). The target directory is node-chosen,
// never payload-supplied, and never overlaps live data paths.
func (r *Runner) Restore(ctx context.Context, payload RestorePayload) (RestoreResult, error) {
	bin, err := r.checkPrereqs()
	if err != nil {
		return RestoreResult{}, err
	}
	if err := ValidateRestore(payload); err != nil {
		return RestoreResult{}, err
	}
	root, err := filepath.Abs(r.restoreRoot())
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%w: restore kökü: %v", ErrRestoreOutside, err)
	}
	target := filepath.Join(root, "rest-"+randHex6())
	// Structural isolation guarantees: fresh dir inside the restore root.
	if _, err := os.Stat(target); err == nil {
		return RestoreResult{}, ErrTargetExists
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("restore hedefi yaratılamadı: %w", err)
	}
	argv := append(r.baseArgv(bin), "restore", payload.Snapshot, "--target", target)
	out, err := r.runArgv(ctx, argv...)
	if err != nil {
		_ = os.RemoveAll(target) // failed restore leaves nothing behind
		return RestoreResult{}, fmt.Errorf("%w: %v", ErrBackupFailed, err)
	}
	res := RestoreResult{
		BackupID: payload.BackupID,
		Target:   target,
		Detail:   fmt.Sprintf("snapshot %s → %s (izole hedef)", payload.Snapshot, target),
	}
	res.Files, res.Bytes = parseRestoreSummary(out)
	return res, nil
}

// parseRestoreSummary extracts counts from restic restore --json output;
// missing fields stay zero (honest unknown rather than invented numbers).
func parseRestoreSummary(out []byte) (int64, int64) {
	var files, bytes int64
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var line struct {
			MessageType string `json:"message_type"`
			TotalFiles  int64  `json:"total_files"`
			TotalBytes  int64  `json:"total_bytes"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if line.MessageType == "summary" {
			files, bytes = line.TotalFiles, line.TotalBytes
		}
	}
	return files, bytes
}

// randHex6 returns 6 lowercase hex chars from crypto/rand.
func randHex6() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%06x", time.Now().UnixNano()%0xffffff)
	}
	return hex.EncodeToString(b[:])
}
