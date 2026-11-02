// Package backup implements Faz 14: Control Plane backup (database-native
// single-transaction logical dump via COPY), integrity verification,
// isolated-target restore drills and node backup record keeping.
//
// Security invariants (docs/phases/phase-14.md):
//   - Control Plane backup and node data backup are SEPARATE record scopes;
//     a node row without node_id is structurally impossible (CHECK constraint).
//   - A backup is not "success" until its integrity verification state is
//     recorded (verify_state). Restore drills always target an ISOLATED
//     database (name allowlist scp_drill_xxxxxxxx) — live data is never
//     touched by this phase.
//   - The encryption master key (CP_SECRETS_KEY) never lives beside the
//     backup repository: ValidateKeySeparation rejects key files inside the
//     backup dir, and dump verification scans the artifact for key material
//     (ErrKeyMaterialInBackup) — fail-closed.
package backup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Scope separates Control Plane backups from node data backups (kriter 1).
const (
	ScopeControlPlane = "control_plane"
	ScopeNode         = "node"
)

// Backup lifecycle states. A backup starts as running; success REQUIRES a
// verify pass (kriter 2). verify_state is honest: unverified means "not
// proven recoverable".
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"

	VerifyUnverified = "unverified"
	VerifyVerified   = "verified"
	VerifyFailed     = "failed"
)

// Dump format marker: every artifact starts with this line and carries a
// footer with the content sha256. Restore rejects foreign files (the node
// dbops adapter uses the same honest marker discipline for db backups).
const FormatMarker = "SCP-BACKUP-V1"

// MaxBackupBytes bounds one Control Plane dump (disk-fill guard). The CP
// database is operational metadata; 512 MiB is a generous ceiling.
const MaxBackupBytes = 512 << 20

// Errors mapped to stable API responses by callers.
var (
	ErrBackupNotFound      = errors.New("backup: yedek bulunamadı")
	ErrInvalidBackupID     = errors.New("backup: yedek kimliği geçersiz")
	ErrUnsafeTarget        = errors.New("backup: restore hedefi izole değil (canlı veri koruması)")
	ErrCorruptBackup       = errors.New("backup: yedek dosyası bozuk (marker/şema doğrulaması başarısız)")
	ErrKeyMaterialInBackup = errors.New("backup: yedek içinde anahtar malzemesi bulundu — doğrulama reddedildi")
	ErrKeyInsideRepo       = errors.New("backup: anahtar dosyası yedek deposu içinde olamaz (kriter 3)")
	ErrBackupLimit         = errors.New("backup: yedek boyut sınırı aşıldı")
	ErrInvalidState        = errors.New("backup: yedek bu işlem için uygun durumda değil")
	ErrDatabaseDenied      = errors.New("backup: izole tatbikat veritabanı oluşturulamadı (CREATEDB yetkisi gerekir)")
)

// Record is one persisted backup entry.
type Record struct {
	ID           string  `json:"id"`
	Scope        string  `json:"scope"`
	NodeID       string  `json:"node_id,omitempty"`
	Kind         string  `json:"kind"`
	Status       string  `json:"status"`
	VerifyState  string  `json:"verify_state"`
	VerifyAt     *string `json:"verify_at,omitempty"`
	VerifyDetail string  `json:"verify_detail,omitempty"`
	SizeBytes    int64   `json:"size_bytes"`
	SHA256       string  `json:"sha256,omitempty"`
	SnapshotID   string  `json:"snapshot_id,omitempty"`
	StoragePath  string  `json:"storage_path,omitempty"`
	TaskID       string  `json:"task_id,omitempty"`
	Detail       string  `json:"detail,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// Drill is one isolated restore rehearsal.
type Drill struct {
	ID             string `json:"id"`
	BackupID       string `json:"backup_id"`
	NodeID         string `json:"node_id,omitempty"`
	Target         string `json:"target"`
	Status         string `json:"status"`
	Detail         string `json:"detail,omitempty"`
	TablesRestored int    `json:"tables_restored"`
	RowsRestored   int64  `json:"rows_restored"`
	TaskID         string `json:"task_id,omitempty"`
	Actor          string `json:"actor,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ValidateKeySeparation enforces kriter 3: the key file must NOT live inside
// the backup repository (nor in any subdirectory). Both paths are resolved
// (symlinks included where possible) before the containment check; an empty
// keyPath is a no-op (env-provided keys never touch disk in this codebase).
func ValidateKeySeparation(backupDir, keyPath string) error {
	if strings.TrimSpace(keyPath) == "" {
		return nil
	}
	absDir, err := filepath.Abs(backupDir)
	if err != nil {
		return fmt.Errorf("backup: yedek dizini çözümlenemedi: %w", err)
	}
	absKey, err := filepath.Abs(keyPath)
	if err != nil {
		return fmt.Errorf("backup: anahtar yolu çözümlenemedi: %w", err)
	}
	// Resolve symlinks on the deepest existing ancestor so a symlinked
	// repository cannot smuggle a key inside.
	absDir = resolveExisting(absDir)
	absKey = resolveExisting(absKey)
	rel, relErr := filepath.Rel(absDir, absKey)
	if relErr != nil {
		// Different roots → not contained.
		return nil
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s", ErrKeyInsideRepo, keyPath)
	}
	return nil
}

// resolveExisting walks up until the path exists and resolves symlinks
// (filepath.EvalSymlinks fails on non-existent tails).
func resolveExisting(p string) string {
	cur := p
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if cur == p {
				return resolved
			}
			// Re-join the unresolved tail.
			tail, rerr := filepath.Rel(cur, p)
			if rerr != nil {
				return p
			}
			return filepath.Join(resolved, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		cur = parent
	}
}

// ValidateBackupID checks the UUID shape of a backup identifier.
func ValidateBackupID(id string) error {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return ErrInvalidBackupID
	}
	for i, r := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ErrInvalidBackupID
		}
	}
	return nil
}

// drillDBPrefix: isolated drill database names are generated, never supplied:
// scp_drill_ + 8 lowercase hex chars. Anything else is rejected.
const drillDBPrefix = "scp_drill_"

// validateDrillDBName enforces the isolated-target allowlist and refuses the
// live database name even if it somehow matches the shape.
func validateDrillDBName(name, liveDB string) error {
	if len(name) != len(drillDBPrefix)+8 || !strings.HasPrefix(name, drillDBPrefix) {
		return fmt.Errorf("%w: %q", ErrUnsafeTarget, name)
	}
	for _, r := range name[len(drillDBPrefix):] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return fmt.Errorf("%w: %q", ErrUnsafeTarget, name)
		}
	}
	if name == liveDB {
		return fmt.Errorf("%w: canlı veritabanı adı tatbikat hedefi olamaz", ErrUnsafeTarget)
	}
	return nil
}
