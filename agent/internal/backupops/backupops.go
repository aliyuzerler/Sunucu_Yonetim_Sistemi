// Package backupops implements the Faz 14 node backup adapter (restic),
// executed ONLY in the privileged broker (server-privd, --backup-enable)
// with fixed argv — never a shell.
//
// Security model (ADR-0018, docs/architecture/backup.md):
//   - The restic repository path is NODE-MANAGED (default
//     /var/backups/server-control/restic) and never crosses the payload.
//   - The repository password lives in a SEPARATE root tree
//     (/etc/server-control/backup/restic-password, 0600) and is passed to
//     restic via RESTIC_PASSWORD_FILE only. Kriter 3: the password file
//     inside the repo directory is a typed refusal (ValidateKeySeparation at
//     every op), and the password NEVER appears in argv, results or audit.
//   - backup.run paths must be absolute, bounded (≤8) and WITHIN the
//     broker-configured source list (default /etc/server-control) — the
//     payload can never widen the backup surface.
//   - backup.restore writes ONLY into a fresh isolated directory under the
//     managed restore root (/var/backups/server-control/restore/rest-<rand>)
//     — live data paths are structurally unreachable (kriter 2).
//   - Results carry snapshot id / counts / verify state only: no password,
//     no repository key material.
package backupops

import (
	"errors"
	"regexp"
	"strings"
)

// Operation names (mirror backend/internal/tasks ops_faz14.go).
const (
	OpBackupRepoInit     = "backup.repo.init"
	OpBackupRun          = "backup.run"
	OpBackupSnapshotList = "backup.snapshot.list"
	OpBackupVerify       = "backup.verify"
	OpBackupRestore      = "backup.restore"
)

// Limits (mirrored in backend/internal/tasks ops_faz14.go).
const (
	MaxPayloadBytes  = 1 << 10
	MaxRunPaths      = 8
	MaxPathLen       = 512
	MaxSnapshotLimit = 100
	MaxOutputBytes   = 2 << 20
)

// Verify states (mirrored).
const (
	VerifyVerified   = "verified"
	VerifyFailed     = "failed"
	VerifyUnverified = "unverified"
)

// Stable errors (mapped to broker categories in exec_unix.go).
var (
	ErrInvalidPaths        = errors.New("geçersiz yedek kaynağı yolu")
	ErrInvalidSnapshot     = errors.New("geçersiz restic snapshot kimliği")
	ErrInvalidBackupID     = errors.New("geçersiz yedek kayıt kimliği")
	ErrInvalidLimit        = errors.New("geçersiz limit")
	ErrRepoMissing         = errors.New("restic deposu yok — önce backup.repo.init")
	ErrRepoPassword        = errors.New("restic parola dosyası yok/okunamıyor")
	ErrKeySeparation       = errors.New("parola dosyası repo dizini içinde olamaz (kriter 3)")
	ErrUnavailable         = errors.New("restic kullanılamıyor")
	ErrTargetExists        = errors.New("izole restore hedefi zaten var")
	ErrRestoreOutside      = errors.New("restore hedefi izole kök dışında")
	ErrBackupFailed        = errors.New("yedekleme başarısız")
	ErrRepoInitFailed      = errors.New("depo kurulumu başarısız")
	ErrSourceNotConfigured = errors.New("yol yapılandırılmış kaynak listesinin dışında")
)

// snapshotRE: restic snapshot ids are 8..64 hex (short id accepted).
var snapshotRE = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// backupIDRE: CP record UUID (lowercase hex, dashed).
var backupIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// RunPayload is the typed schema for backup.run (mirror of the CP validator).
type RunPayload struct {
	Paths []string `json:"paths,omitempty"`
}

// SnapshotListPayload is the typed schema for backup.snapshot.list.
type SnapshotListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// VerifyPayload is the typed schema for backup.verify.
type VerifyPayload struct {
	BackupID string `json:"backup_id"`
}

// RestorePayload is the typed schema for backup.restore. NOTE: there is
// deliberately NO target/path field — the node picks the isolated directory.
type RestorePayload struct {
	BackupID string `json:"backup_id"`
	Snapshot string `json:"snapshot"`
}

// ValidateRun mirrors the CP validator: ≤8 paths, absolute, ≤512 chars,
// no "..", no control chars.
func ValidateRun(p RunPayload) error {
	if len(p.Paths) > MaxRunPaths {
		return ErrInvalidPaths
	}
	for _, path := range p.Paths {
		if path == "" || len(path) > MaxPathLen {
			return ErrInvalidPaths
		}
		if !strings.HasPrefix(path, "/") {
			return ErrInvalidPaths
		}
		if strings.Contains(path, "..") {
			return ErrInvalidPaths
		}
		for _, r := range path {
			if r < 0x20 {
				return ErrInvalidPaths
			}
		}
	}
	return nil
}

// ValidateSnapshotList mirrors the CP validator (limit 0..100).
func ValidateSnapshotList(p SnapshotListPayload) error {
	if p.Limit < 0 || p.Limit > MaxSnapshotLimit {
		return ErrInvalidLimit
	}
	return nil
}

// ValidateVerify mirrors the CP validator (UUID shape).
func ValidateVerify(p VerifyPayload) error {
	if !backupIDRE.MatchString(p.BackupID) {
		return ErrInvalidBackupID
	}
	return nil
}

// ValidateRestore mirrors the CP validator.
func ValidateRestore(p RestorePayload) error {
	if !backupIDRE.MatchString(p.BackupID) {
		return ErrInvalidBackupID
	}
	if !snapshotRE.MatchString(p.Snapshot) {
		return ErrInvalidSnapshot
	}
	return nil
}

// pathsWithinSources ensures every requested path sits inside one of the
// broker-configured sources (prefix on cleaned absolute paths).
func pathsWithinSources(paths, sources []string) error {
	if len(sources) == 0 {
		return ErrSourceNotConfigured
	}
	for _, p := range paths {
		ok := false
		for _, src := range sources {
			if p == src || strings.HasPrefix(p, strings.TrimSuffix(src, "/")+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return ErrSourceNotConfigured
		}
	}
	return nil
}
