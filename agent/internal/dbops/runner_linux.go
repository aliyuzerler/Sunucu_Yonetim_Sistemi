//go:build linux

package dbops

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Default socket/backup locations (Linux). Injectable for hermetic tests.
const (
	DefaultPGSocket    = "/run/postgresql/.s.PGSQL.5432"
	DefaultMySQLSocket = "/run/mysqld/mysqld.sock"
	DefaultBackupDir   = "/var/backups/srvctl/db"
)

// Runner executes database operations against LOCAL instances on the node.
// Tool paths and sockets are injectable for hermetic tests; the zero value
// looks binaries up on PATH and uses the default sockets.
type Runner struct {
	Runuser     string // /usr/sbin/runuser ("" → LookPath)
	Psql        string // psql ("" → LookPath)
	PgDump      string // pg_dump ("" → LookPath)
	Mysql       string // mysql ("" → LookPath)
	MysqlDump   string // mysqldump ("" → LookPath)
	Systemctl   string // systemctl ("" → LookPath)
	PGSocket    string // default /run/postgresql/.s.PGSQL.5432
	MySQLSocket string // default /run/mysqld/mysqld.sock
	BackupDir   string // default /var/backups/srvctl/db
	Timeout     time.Duration
	// now is overridable for tests.
	now func() time.Time
}

func (r *Runner) execTimeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 120 * time.Second
}

func (r *Runner) pgSocket() string {
	if r.PGSocket != "" {
		return r.PGSocket
	}
	return DefaultPGSocket
}

func (r *Runner) mySocket() string {
	if r.MySQLSocket != "" {
		return r.MySQLSocket
	}
	return DefaultMySQLSocket
}

func (r *Runner) backupDir() string {
	if r.BackupDir != "" {
		return r.BackupDir
	}
	return DefaultBackupDir
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

// runArgv executes one fixed program with an argument vector (no shell).
// stderr detail is intentionally NOT returned (log safety); errors carry a
// short program context only.
func runArgv(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary + argv, no user shell
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		_ = msg // honest typed errors; raw server messages stay out of results
		return nil, fmt.Errorf("%s başarısız: %w", filepath.Base(bin), err)
	}
	return stdout.Bytes(), nil
}

// pgArgs builds the fixed psql invocation: run as the postgres OS user via
// runuser (root broker → peer auth as postgres), socket-directory local,
// unaligned tab output, stop on first error. sql is the single command
// string (identifiers pre-validated by the package allowlist REs).
func (r *Runner) pgArgs(sql string) (bin string, args []string, ok bool) {
	ru, ok := resolve(r.Runuser, "runuser")
	if !ok {
		return "", nil, false
	}
	ps, ok := resolve(r.Psql, "psql")
	if !ok {
		return "", nil, false
	}
	args = []string{"-u", "postgres", "--", ps,
		"-h", filepath.Dir(r.pgSocket()),
		"-X", "-A", "-t", "-v", "ON_ERROR_STOP=1",
		"-c", sql}
	return ru, args, true
}

// myArgs builds the fixed mysql invocation: root over the local unix socket
// (Debian/Ubuntu/MariaDB unix_socket auth plugin — no password on argv).
func (r *Runner) myArgs(sql string) (bin string, args []string, ok bool) {
	my, ok := resolve(r.Mysql, "mysql")
	if !ok {
		return "", nil, false
	}
	args = []string{"-S", r.mySocket(), "-N", "-B", "-e", sql}
	return my, args, true
}

// query runs one SQL string against the kind's local client and returns
// trimmed stdout.
func (r *Runner) query(ctx context.Context, kind, sql string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	var bin string
	var args []string
	var ok bool
	if kind == KindPostgres {
		bin, args, ok = r.pgArgs(sql)
	} else {
		bin, args, ok = r.myArgs(sql)
	}
	if !ok {
		return nil, ErrUnavailable
	}
	return runArgv(ctx, bin, args...)
}

// sqlExists probes existence (database or user) and parses a 0/1 answer.
func (r *Runner) sqlExists(ctx context.Context, kind, what, name, host string) (bool, error) {
	out, err := r.query(ctx, kind, ExistsSQL(kind, what, name, host))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

// Instance is one detected local server instance.
type Instance struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	Socket  string `json:"socket"`
	Active  string `json:"active,omitempty"` // systemctl is-active result
}

// InstancesList detects reachable local instances (version probe = proof).
func (r *Runner) InstancesList(ctx context.Context) ([]Instance, error) {
	var out []Instance
	for _, k := range []string{KindPostgres, KindMySQL} {
		socket := r.pgSocket()
		if k == KindMySQL {
			socket = r.mySocket()
		}
		if _, err := os.Stat(socket); err != nil {
			continue // not installed / not listening on the default socket
		}
		inst := Instance{Kind: k, Socket: socket, Active: r.unitState(ctx, k)}
		if v, err := r.query(ctx, k, VersionSQL(k)); err == nil {
			inst.Version = firstField(string(v))
		}
		out = append(out, inst)
	}
	return out, nil
}

// unitState reports systemctl is-active for the kind's well-known unit
// (detection only — dbops never starts/stops units).
func (r *Runner) unitState(ctx context.Context, kind string) string {
	sc, ok := resolve(r.Systemctl, "systemctl")
	if !ok {
		return ""
	}
	unit := "postgresql"
	if kind == KindMySQL {
		unit = "mariadb"
	}
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx2, sc, "is-active", unit) //nolint:gosec // fixed argv
	out, err := c.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func firstField(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i > 0 {
		s = s[:i]
	}
	return s
}

// DatabaseInfo is one database row (name + server-rendered size).
type DatabaseInfo struct {
	Name string `json:"name"`
	Size string `json:"size,omitempty"`
}

// DatabasesList returns the instance's databases (bounded).
func (r *Runner) DatabasesList(ctx context.Context, kind string, limit int) ([]DatabaseInfo, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if limit < 0 || limit > MaxList {
		return nil, ErrLimit
	}
	out, err := r.query(ctx, kind, ListSQL(kind, "database"))
	if err != nil {
		return nil, err
	}
	var rows []DatabaseInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		row := DatabaseInfo{Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			row.Size = strings.TrimSpace(parts[1])
		}
		rows = append(rows, row)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// UserInfo is one login account (NEVER carries password material).
type UserInfo struct {
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
}

// UsersList returns the instance's login accounts (bounded).
func (r *Runner) UsersList(ctx context.Context, kind string, limit int) ([]UserInfo, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if limit < 0 || limit > MaxList {
		return nil, ErrLimit
	}
	out, err := r.query(ctx, kind, ListSQL(kind, "user"))
	if err != nil {
		return nil, err
	}
	var rows []UserInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		row := UserInfo{Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			row.Host = strings.TrimSpace(parts[1])
		}
		rows = append(rows, row)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// DatabaseCreate creates a database (idempotent: existing → no-op, no
// error). PG owner is optional (validated identifier).
func (r *Runner) DatabaseCreate(ctx context.Context, kind, name, owner string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if IsProtectedDatabase(name) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, name)
	}
	if owner != "" {
		if !ValidName(owner) {
			return nil, fmt.Errorf("%w: owner %q", ErrInvalidName, owner)
		}
		if IsProtectedUser(owner) {
			return nil, fmt.Errorf("%w: owner %q", ErrProtected, owner)
		}
	}
	exists, err := r.sqlExists(ctx, kind, "database", name, "")
	if err != nil {
		return nil, err
	}
	if exists {
		return map[string]any{"kind": kind, "database": name, "created": false, "already_exists": true}, nil
	}
	if _, err := r.query(ctx, kind, DatabaseCreateSQL(kind, name, owner)); err != nil {
		return nil, err
	}
	return map[string]any{"kind": kind, "database": name, "created": true}, nil
}

// DatabaseDelete drops a database (destructive). System/panel names are
// refused; missing databases are a typed error (honest no silent success).
func (r *Runner) DatabaseDelete(ctx context.Context, kind, name string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if IsProtectedDatabase(name) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, name)
	}
	exists, err := r.sqlExists(ctx, kind, "database", name, "")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: veritabanı %q", ErrNotFound, name)
	}
	if _, err := r.query(ctx, kind, DatabaseDeleteSQL(kind, name)); err != nil {
		return nil, err
	}
	return map[string]any{"kind": kind, "database": name, "deleted": true}, nil
}

// UserCreateResult is the one-time credential surface of db.user.create.
type UserCreateResult struct {
	Kind        string `json:"kind"`
	User        UserInfo
	Credentials *UserCredentials `json:"credentials,omitempty"`
}

// UserCredentials carries the generated password EXACTLY ONCE (node →
// control-plane scrubber); fingerprint lets the operator recognize the
// secret without exposing it.
type UserCredentials struct {
	Password    string `json:"password"`
	Fingerprint string `json:"fingerprint"`
}

// UserCreate creates a login account with a node-generated random password
// (idempotent: existing user → no-op, password NOT rotated).
func (r *Runner) UserCreate(ctx context.Context, kind, name, host string) (UserCreateResult, error) {
	res := UserCreateResult{Kind: kind}
	if !ValidKind(kind) {
		return res, ErrInvalidKind
	}
	if !ValidName(name) {
		return res, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if IsProtectedUser(name) {
		return res, fmt.Errorf("%w: %q", ErrProtected, name)
	}
	if kind == KindMySQL {
		if host == "" {
			host = "localhost"
		}
		if !ValidHost(host) {
			return res, fmt.Errorf("%w: %q", ErrInvalidHost, host)
		}
	}
	exists, err := r.sqlExists(ctx, kind, "user", name, host)
	if err != nil {
		return res, err
	}
	if exists {
		res.User = UserInfo{Name: name, Host: host}
		return res, nil // already_exists: result carries NO credentials block
	}
	pw, err := GeneratePassword()
	if err != nil {
		return res, fmt.Errorf("şifre üretilemedi: %w", err)
	}
	if _, err := r.query(ctx, kind, UserCreateSQL(kind, name, host, pw)); err != nil {
		return res, err
	}
	res.User = UserInfo{Name: name, Host: host}
	res.Credentials = &UserCredentials{Password: pw, Fingerprint: fingerprintSHA(pw)}
	return res, nil
}

func fingerprintSHA(pw string) string {
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])[:16]
}

// UserDelete drops a login account (destructive).
func (r *Runner) UserDelete(ctx context.Context, kind, name, host string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if IsProtectedUser(name) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, name)
	}
	if kind == KindMySQL {
		if host == "" {
			host = "localhost"
		}
		if !ValidHost(host) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHost, host)
		}
	}
	exists, err := r.sqlExists(ctx, kind, "user", name, host)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: kullanıcı %q", ErrNotFound, name)
	}
	if _, err := r.query(ctx, kind, UserDeleteSQL(kind, name, host)); err != nil {
		return nil, err
	}
	return map[string]any{"kind": kind, "user": map[string]any{"name": name, "host": host}, "deleted": true}, nil
}

// GrantSet forces the desired privilege level (revokes first — desired-state
// convergence).
func (r *Runner) GrantSet(ctx context.Context, kind, user, host, database, level string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(user) || !ValidName(database) {
		return nil, ErrInvalidName
	}
	if IsProtectedUser(user) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, user)
	}
	if !ValidLevel(level) {
		return nil, ErrInvalidLevel
	}
	if kind == KindMySQL {
		if host == "" {
			host = "localhost"
		}
		if !ValidHost(host) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHost, host)
		}
	}
	uExists, err := r.sqlExists(ctx, kind, "user", user, host)
	if err != nil {
		return nil, err
	}
	if !uExists {
		return nil, fmt.Errorf("%w: kullanıcı %q", ErrNotFound, user)
	}
	dExists, err := r.sqlExists(ctx, kind, "database", database, "")
	if err != nil {
		return nil, err
	}
	if !dExists {
		return nil, fmt.Errorf("%w: veritabanı %q", ErrNotFound, database)
	}
	// One session, ordered statements (psql -c keeps them in one implicit
	// transaction; mysql -e runs them sequentially as authored).
	sql := strings.Join(GrantSetSQL(kind, user, host, database, level), " ")
	if _, err := r.query(ctx, kind, sql); err != nil {
		return nil, err
	}
	return map[string]any{
		"kind": kind, "user": map[string]any{"name": user, "host": host},
		"database": database, "level": level, "set": true,
	}, nil
}

// BackupInfo describes one managed backup file.
type BackupInfo struct {
	File    string `json:"file"`
	Bytes   int64  `json:"bytes"`
	Created string `json:"created"` // RFC3339 (mtime)
}

// backupKindDir returns the per-kind managed directory.
func (r *Runner) backupKindDir(kind string) string {
	return filepath.Join(r.backupDir(), kind)
}

// Backup creates a DB-native consistent dump (criterion 3):
//   - postgres: pg_dump reads ONE MVCC snapshot (consistent while writers
//     keep running);
//   - mysql: mysqldump --single-transaction (InnoDB consistent snapshot).
//
// The file is written with our marker as the FIRST line; restore refuses
// markerless files. Partial files are removed on any failure.
func (r *Runner) Backup(ctx context.Context, kind, database string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(database) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, database)
	}
	if IsProtectedDatabase(database) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, database)
	}
	exists, err := r.sqlExists(ctx, kind, "database", database, "")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: veritabanı %q", ErrNotFound, database)
	}

	dir := r.backupKindDir(kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("yedek dizini: %w", err)
	}
	if err := r.checkCapacity(dir); err != nil {
		return nil, err
	}

	file := BackupFileName(database, r.nowFunc())
	path := filepath.Join(dir, file)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is derived from validated components
	if err != nil {
		if os.IsExist(err) {
			// Same-second collision: refuse honestly (retry converges).
			return nil, fmt.Errorf("%w: %q zaten var", ErrConflict, file)
		}
		return nil, fmt.Errorf("yedek dosyası: %w", err)
	}
	started := time.Now()
	shaW := sha256.New()
	mw := io.MultiWriter(f, shaW)
	syncErr := error(nil)
	var dumpErr error
	func() {
		defer f.Close()
		if _, err := mw.Write([]byte(BackupMarker + "\n")); err != nil {
			dumpErr = err
			return
		}
		if err := r.streamDump(ctx, kind, database, mw); err != nil {
			dumpErr = err
		}
		// Sync BEFORE close (the deferred Close runs when this func returns).
		if dumpErr == nil {
			syncErr = f.Sync()
		}
	}()
	if dumpErr != nil {
		_ = os.Remove(path) // no partial backups on disk
		return nil, dumpErr
	}
	if syncErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("yedek sync: %w", syncErr)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("yedek boyutu: %w", err)
	}
	return map[string]any{
		"kind": kind, "database": database,
		"file":     file,
		"bytes":    st.Size(),
		"sha256":   hex.EncodeToString(shaW.Sum(nil)),
		"duration": time.Since(started).Round(time.Millisecond).String(),
	}, nil
}

// checkCapacity enforces the honest bounds: ≤ MaxBackupFiles per kind and
// ≥ MinFreeSpace bytes available (statfs).
func (r *Runner) checkCapacity(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("yedek dizini okunamadı: %w", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), BackupSuffix) {
			n++
		}
	}
	if n >= MaxBackupFiles {
		return fmt.Errorf("%w: %d yedek dosyası sınırda", ErrLimit, MaxBackupFiles)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return fmt.Errorf("disk alanı: %w", err)
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	if avail < MinFreeSpace {
		return fmt.Errorf("%w: %.1f MiB", ErrNoSpace, float64(avail)/(1<<20))
	}
	return nil
}

// streamDump pipes the native dump tool's stdout into w (no shell, no
// temp files, memory-bounded by streaming).
func (r *Runner) streamDump(ctx context.Context, kind, database string, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	var cmd *exec.Cmd
	if kind == KindPostgres {
		ru, ok := resolve(r.Runuser, "runuser")
		if !ok {
			return ErrUnavailable
		}
		pd, ok := resolve(r.PgDump, "pg_dump")
		if !ok {
			return ErrUnavailable
		}
		cmd = exec.CommandContext(ctx, ru, "-u", "postgres", "--", pd, //nolint:gosec // fixed argv
			"-h", filepath.Dir(r.pgSocket()),
			"--format=plain", "--no-owner", "--no-privileges", database)
	} else {
		md, ok := resolve(r.MysqlDump, "mysqldump")
		if !ok {
			return ErrUnavailable
		}
		// --single-transaction: InnoDB consistent snapshot (criterion 3);
		// --quick: row streaming (no full-table memory); --no-tablespaces:
		// avoids the MySQL 8 PROCESS privilege requirement.
		cmd = exec.CommandContext(ctx, md, "--single-transaction", "--quick", //nolint:gosec // fixed argv
			"--no-tablespaces", "-S", r.mySocket(), database)
	}
	var stderr bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = stderr.String() // typed error only; server text stays out of results
		return fmt.Errorf("dump başarısız: %w", err)
	}
	return nil
}

// BackupsList returns the managed backup files of a kind (marker-verified).
func (r *Runner) BackupsList(ctx context.Context, kind string, limit int) ([]BackupInfo, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if limit < 0 || limit > MaxList {
		return nil, ErrLimit
	}
	dir := r.backupKindDir(kind)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil // no backups yet — honest empty result
	}
	if err != nil {
		return nil, fmt.Errorf("yedek dizini: %w", err)
	}
	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), BackupSuffix) || !ValidBackupName(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		marked, err := hasBackupMarker(path)
		if err != nil || !marked {
			continue // foreign file in our dir is NOT listed (invisible)
		}
		out = append(out, BackupInfo{
			File:    e.Name(),
			Bytes:   st.Size(),
			Created: st.ModTime().UTC().Format(time.RFC3339),
		})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// hasBackupMarker reads the first line and compares it with the marker.
func hasBackupMarker(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // path from validated dir listing
	if err != nil {
		return false, err
	}
	defer f.Close()
	br := bufio.NewReader(io.LimitReader(f, 512))
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return strings.TrimRight(line, "\r\n") == BackupMarker, nil
}

// Restore loads a MANAGED backup into an existing database (destructive).
// Only marker-bearing files inside the managed directory are accepted.
func (r *Runner) Restore(ctx context.Context, kind, database, backup string) (map[string]any, error) {
	if !ValidKind(kind) {
		return nil, ErrInvalidKind
	}
	if !ValidName(database) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, database)
	}
	if IsProtectedDatabase(database) {
		return nil, fmt.Errorf("%w: %q", ErrProtected, database)
	}
	if !ValidBackupName(backup) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidBackup, backup)
	}
	dir := r.backupKindDir(kind)
	path := filepath.Join(dir, backup)
	// Defense in depth: the joined path must stay inside the managed dir
	// (the RE already forbids separators; the prefix check is belt).
	if filepath.Dir(path) != dir {
		return nil, ErrInvalidBackup
	}
	marked, err := hasBackupMarker(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: yedek %q", ErrNotFound, backup)
	}
	if err != nil {
		return nil, fmt.Errorf("yedek okunamadı: %w", err)
	}
	if !marked {
		return nil, ErrForeignBackup
	}
	dExists, err := r.sqlExists(ctx, kind, "database", database, "")
	if err != nil {
		return nil, err
	}
	if !dExists {
		return nil, fmt.Errorf("%w: veritabanı %q (önce oluşturun)", ErrNotFound, database)
	}
	started := time.Now()
	if err := r.streamRestore(ctx, kind, database, path); err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("yedek boyutu: %w", err)
	}
	return map[string]any{
		"kind": kind, "database": database, "file": backup,
		"bytes": st.Size(), "restored": true,
		"duration": time.Since(started).Round(time.Millisecond).String(),
	}, nil
}

// streamRestore feeds the marker-bearing SQL file to the native client
// (psql -f / mysql stdin). On failure the target database may hold partial
// data — the task result reports the failure and the operator re-restores
// (documented operational risk, ADR-0014).
func (r *Runner) streamRestore(ctx context.Context, kind, database, path string) error {
	ctx, cancel := context.WithTimeout(ctx, r.execTimeout())
	defer cancel()
	f, err := os.Open(path) //nolint:gosec // path validated above
	if err != nil {
		return fmt.Errorf("yedek açılamadı: %w", err)
	}
	defer f.Close()

	// CI-28 (gerçek-host kanıtı): yedek dosyası root'a ait 0700 dizinde
	// durur — `runuser -u postgres -- psql -f <path>` postgres OS
	// kullanıcısı dosyayı OKUYAMAZ (exit 1). Dosya root tarafından açılır
	// ve STDIN üzerinden psql'e akıtılır; izin gevşetilmez (yedek 0600
	// kalır), psql stdin'i SQL olarak işler.
	var cmd *exec.Cmd
	if kind == KindPostgres {
		ru, ok := resolve(r.Runuser, "runuser")
		if !ok {
			return ErrUnavailable
		}
		ps, ok := resolve(r.Psql, "psql")
		if !ok {
			return ErrUnavailable
		}
		cmd = exec.CommandContext(ctx, ru, "-u", "postgres", "--", ps, //nolint:gosec // fixed argv
			"-h", filepath.Dir(r.pgSocket()),
			"-X", "-v", "ON_ERROR_STOP=1", "-d", database)
		cmd.Stdin = f
	} else {
		my, ok := resolve(r.Mysql, "mysql")
		if !ok {
			return ErrUnavailable
		}
		cmd = exec.CommandContext(ctx, my, "-S", r.mySocket(), database) //nolint:gosec // fixed argv
		cmd.Stdin = f
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = stderr.String()
		return fmt.Errorf("restore başarısız: %w", err)
	}
	return nil
}
