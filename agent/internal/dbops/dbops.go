// Package dbops implements the Faz 10 database management adapter
// (MariaDB/MySQL + PostgreSQL), executed ONLY in the privileged broker
// (server-privd, --db-enable) with fixed argv — never a shell.
//
// Security model (ADR-0014, docs/architecture/db-files.md):
//   - LOCAL instances only: the adapter talks to node-local unix sockets
//     (/run/postgresql, /run/mysqld/mysqld.sock). The payload schema has NO
//     host/port/DSN fields at all, so the control plane's own PostgreSQL
//     instance is structurally unreachable from here (acceptance
//     criterion 1: panel DB ≠ managed customer DB instances).
//   - All identifiers are validated against a strict lowercase allowlist RE
//     BEFORE embedding into SQL (belt: quote with "" / “; suspenders: the
//     RE makes quoting redundant). Passwords use an alphanumeric charset
//     (safe inside single quotes) and are generated on the node.
//   - Passwords are returned ONCE in the task result; the control plane
//     seals them (internal/dbcred) before persisting the result. They are
//     never logged, never re-displayed (criterion 2).
//   - Backups are DB-native consistent (criterion 3): pg_dump reads a
//     single MVCC snapshot; mysqldump runs with --single-transaction
//     (InnoDB). Every backup file starts with our marker line and restore
//     REFUSES markerless files (no arbitrary-file restore).
//   - System databases and the control-plane database names are PROTECTED:
//     create/delete on them is a typed refusal.
package dbops

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"time"
)

// Operation names (mirror backend/internal/tasks ops_faz10.go).
const (
	OpDBInstanceList   = "db.instance.list"
	OpDBDatabaseList   = "db.database.list"
	OpDBDatabaseCreate = "db.database.create"
	OpDBDatabaseDelete = "db.database.delete"
	OpDBUserList       = "db.user.list"
	OpDBUserCreate     = "db.user.create"
	OpDBUserDelete     = "db.user.delete"
	OpDBGrantSet       = "db.grant.set"
	OpDBBackup         = "db.backup"
	OpDBBackupList     = "db.backup.list"
	OpDBRestore        = "db.restore"
)

// Kinds (closed enum) and grant levels (closed enum).
const (
	KindPostgres = "postgres"
	KindMySQL    = "mysql"

	LevelReadonly  = "readonly"
	LevelReadWrite = "readwrite"
	LevelAll       = "all"
)

// Limits (mirrored in backend/internal/tasks ops_faz10.go).
const (
	MaxNameLen      = 63
	MaxHostLen      = 255
	MaxList         = 500
	MaxBackupFiles  = 200
	PasswordLength  = 32
	MinFreeSpace    = 64 << 20 // 64 MiB: honest floor before a dump starts
	MaxPayloadBytes = 1 << 10
)

// BackupMarker is the first line of EVERY backup file dbops writes.
// Restore refuses files without it (never restores a foreign file).
const BackupMarker = "-- server-control managed db backup (db.backup)"

// BackupSuffix bounds the managed backup directory.
const BackupSuffix = ".sql"

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidKind   = errors.New("db türü postgres|mysql olmalı")
	ErrInvalidName   = errors.New("geçersiz veritabanı/kullanıcı adı")
	ErrInvalidHost   = errors.New("geçersiz mysql host bileşeni")
	ErrInvalidLevel  = errors.New("yetki seviyesi readonly|readwrite|all olmalı")
	ErrInvalidBackup = errors.New("geçersiz yedek dosya adı")
	ErrNotFound      = errors.New("kayıt bulunamadı")
	ErrProtected     = errors.New("korumalı ad — sistem/panel veritabanı dokunulmaz")
	ErrConflict      = errors.New("veritabanı durumu çelişkili")
	ErrLimit         = errors.New("sınır aşıldı")
	ErrNoSpace       = errors.New("disk alanı yetersiz (yedek için < 64 MiB)")
	ErrUnavailable   = errors.New("veritabanı istemcisi/soketi kullanılamıyor")
	ErrForeignBackup = errors.New("yabancı yedek dosyası (marker yok) — geri yüklenmez")
)

// nameRE: strict identifier allowlist (databases AND users). Lowercase
// start, lowercase alnum + underscore, ≤ 63 chars. Anything the RE rejects
// never reaches a SQL string.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// hostRE: MySQL user host component (closed charset, no quotes/spaces;
// bare `%` allowed for the documented any-host form).
var hostRE = regexp.MustCompile(`^(%|[A-Za-z0-9][A-Za-z0-9._-]{0,254})$`)

// backupNameRE: filenames inside the managed backup dir (no separators, no
// leading dash, single extension). Paths are NEVER accepted from payloads.
var backupNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.sql$`)

// protectedDatabases: system schemas + the documented control-plane names
// (deploy default server_control; srvctl/serverctl conventions). If the
// operator renamed the panel DB to something exotic, protection relies on
// discipline — documented residual risk in ADR-0014.
var protectedDatabases = map[string]bool{
	"postgres": true, "template0": true, "template1": true,
	"information_schema": true, "performance_schema": true,
	"sys": true, "mysql": true,
	"server_control": true, "serverctl": true, "srvctl": true,
}

// protectedDatabasesPrefix: panel-DB naming conventions.
var protectedDatabasePrefixes = []string{"srvctl_", "serverctl_"}

// protectedUsers: superuser/service accounts must never be created or
// dropped through the panel.
var protectedUsers = map[string]bool{
	"root": true, "postgres": true, "mysql": true, "admin": true,
	"debian-sys-maint": true,
	"mysql.sys":        true, "mysql.session": true, "mysql.infoschema": true,
	"serverctl": true, "server_control": true, "srvctl": true,
}

// ValidKind reports whether kind is in the closed enum.
func ValidKind(kind string) bool { return kind == KindPostgres || kind == KindMySQL }

// ValidName reports whether the identifier passes the allowlist RE.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// ValidHost reports whether the MySQL host component passes the allowlist.
func ValidHost(host string) bool { return hostRE.MatchString(host) }

// ValidLevel reports whether the grant level is in the closed enum.
func ValidLevel(level string) bool {
	return level == LevelReadonly || level == LevelReadWrite || level == LevelAll
}

// ValidBackupName reports whether the backup filename is acceptable.
func ValidBackupName(name string) bool { return backupNameRE.MatchString(name) }

// IsProtectedDatabase reports whether a database name must never be
// created/deleted through the panel.
func IsProtectedDatabase(name string) bool {
	if protectedDatabases[name] {
		return true
	}
	for _, p := range protectedDatabasePrefixes {
		if len(name) > len(p) && name[:len(p)] == p {
			return true
		}
	}
	return false
}

// IsProtectedUser reports whether a user name is a service/super account.
func IsProtectedUser(name string) bool { return protectedUsers[name] }

// ValidLimit bounds list payloads (0 → server default 200).
func ValidLimit(limit int) bool { return limit >= 0 && limit <= MaxList }

// GeneratePassword returns a cryptographically random alphanumeric password
// (32 chars). The charset is deliberate: safe to embed in single-quoted SQL
// literals, safe on argv echo (never on argv anyway), unambiguous.
func GeneratePassword() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, PasswordLength)
	max := big.NewInt(int64(len(charset)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[n.Int64()]
	}
	return string(out), nil
}

// QuotePG renders a validated identifier as a quoted PostgreSQL identifier.
// Callers MUST pass ValidName() first; the function is panic-free but the
// caller is responsible for validation (documented contract).
func QuotePG(name string) string { return `"` + name + `"` }

// QuoteMySQL renders a validated identifier as a quoted MySQL identifier.
func QuoteMySQL(name string) string { return "`" + name + "`" }

// timestamp returns the UTC backup timestamp component.
func timestamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// BackupFileName derives the managed backup file name.
func BackupFileName(database string, t time.Time) string {
	return database + "-" + timestamp(t) + BackupSuffix
}

// PG helper SQL fragments (identifiers already validated).
const (
	pgCheckDB   = `SELECT 1 FROM pg_database WHERE datname = `
	pgCheckUser = `SELECT 1 FROM pg_roles WHERE rolname = `
	pgListDBs   = `SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database WHERE NOT datistemplate ORDER BY 1`
	pgListUsers = `SELECT rolname FROM pg_roles WHERE rolcanlogin ORDER BY 1`
	pgVersion   = `SELECT version()`
)

const (
	myListDBs   = `SELECT schema_name FROM information_schema.schemata ORDER BY 1`
	myListUsers = `SELECT User, Host FROM mysql.user ORDER BY User, Host`
	myVersion   = `SELECT VERSION()`
	myCheckDB   = `SELECT 1 FROM information_schema.schemata WHERE schema_name = `
	myCheckUser = `SELECT 1 FROM mysql.user WHERE User = `
)

// GrantSetSQL returns the statement sequence that FORCES the desired level
// (desired-state semantics: previous level is revoked first, so re-running
// converges — idempotent). Statements are executed in one session.
func GrantSetSQL(kind, user, host, database, level string) []string {
	if kind == KindPostgres {
		u, d := QuotePG(user), QuotePG(database)
		base := []string{
			`REVOKE ALL PRIVILEGES ON DATABASE ` + d + ` FROM ` + u + `;`,
			`REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM ` + u + `;`,
			`REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM ` + u + `;`,
			`GRANT CONNECT ON DATABASE ` + d + ` TO ` + u + `;`,
			`GRANT USAGE ON SCHEMA public TO ` + u + `;`,
		}
		switch level {
		case LevelReadonly:
			return append(base,
				`GRANT SELECT ON ALL TABLES IN SCHEMA public TO `+u+`;`,
				`GRANT SELECT, USAGE ON ALL SEQUENCES IN SCHEMA public TO `+u+`;`)
		case LevelReadWrite:
			return append(base,
				`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO `+u+`;`,
				`GRANT SELECT, USAGE ON ALL SEQUENCES IN SCHEMA public TO `+u+`;`)
		case LevelAll:
			return append(base,
				`GRANT ALL PRIVILEGES ON DATABASE `+d+` TO `+u+`;`,
				`GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO `+u+`;`,
				`GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO `+u+`;`)
		}
		return base
	}
	// MySQL/MariaDB: user is 'u'@'h'; db.* scope covers future tables.
	ident := `'` + user + `'@'` + host + `'`
	db := QuoteMySQL(database)
	var grant string
	switch level {
	case LevelReadonly:
		grant = `GRANT SELECT ON ` + db + `.* TO ` + ident + `;`
	case LevelReadWrite:
		grant = `GRANT SELECT, INSERT, UPDATE, DELETE ON ` + db + `.* TO ` + ident + `;`
	case LevelAll:
		grant = `GRANT ALL PRIVILEGES ON ` + db + `.* TO ` + ident + `;`
	}
	return []string{
		`REVOKE ALL PRIVILEGES ON ` + db + `.* FROM ` + ident + `;`,
		grant,
	}
}

// DatabaseCreateSQL renders the create statement (idempotent form).
func DatabaseCreateSQL(kind, name, owner string) string {
	if kind == KindPostgres {
		// CREATE DATABASE cannot run inside a transaction block; existence
		// is pre-checked by the runner (converges on retry).
		sql := `CREATE DATABASE ` + QuotePG(name)
		if owner != "" {
			sql += ` OWNER ` + QuotePG(owner)
		}
		return sql
	}
	return `CREATE DATABASE IF NOT EXISTS ` + QuoteMySQL(name)
}

// DatabaseDeleteSQL renders the delete statement.
func DatabaseDeleteSQL(kind, name string) string {
	if kind == KindPostgres {
		return `DROP DATABASE ` + QuotePG(name)
	}
	return `DROP DATABASE ` + QuoteMySQL(name)
}

// UserCreateSQL renders the create statement with the generated password.
// The password charset is alphanumeric → single-quote embedding is safe.
func UserCreateSQL(kind, name, host, password string) string {
	if kind == KindPostgres {
		return `CREATE ROLE ` + QuotePG(name) + ` LOGIN PASSWORD '` + password + `'`
	}
	return `CREATE USER '` + name + `'@'` + host + `' IDENTIFIED BY '` + password + `'`
}

// UserDeleteSQL renders the drop statement.
func UserDeleteSQL(kind, name, host string) string {
	if kind == KindPostgres {
		return `DROP ROLE ` + QuotePG(name)
	}
	return `DROP USER '` + name + `'@'` + host + `'`
}

// ExistsSQL returns the existence probe for a database or user.
func ExistsSQL(kind, what, name, host string) string {
	if kind == KindPostgres {
		if what == "user" {
			return pgCheckUser + `'` + name + `'`
		}
		return pgCheckDB + `'` + name + `'`
	}
	if what == "user" {
		return myCheckUser + `'` + name + `' AND Host = '` + host + `'`
	}
	return myCheckDB + `'` + name + `'`
}

// ListSQL returns the list query for databases/users.
func ListSQL(kind, what string) string {
	if kind == KindPostgres {
		if what == "user" {
			return pgListUsers
		}
		return pgListDBs
	}
	if what == "user" {
		return myListUsers
	}
	return myListDBs
}

// VersionSQL returns the version probe.
func VersionSQL(kind string) string {
	if kind == KindPostgres {
		return pgVersion
	}
	return myVersion
}
