package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/db/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service implements the Control Plane backup use cases. It is wired in
// main.go with the live DATABASE_URL, the artifact directory and the embedded
// migrations (schema source of truth for restore drills).
type Service struct {
	Store Store
	// BackupDir holds Control Plane dump artifacts (0700). The encryption
	// master key NEVER lives here (kriter 3).
	BackupDir string
	// DatabaseURL is the live connection string; drill targets are derived
	// from the same server with an isolated database name.
	DatabaseURL string
	// KeyMaterial (optional) is the base64 CP_SECRETS_KEY value; when set,
	// every verification scans artifacts for it (ErrKeyMaterialInBackup).
	KeyMaterial string
	// SecretsKeyFile (optional) is an operator-configured key file path;
	// ValidateKeySeparation refuses paths inside BackupDir.
	SecretsKeyFile string
	// Migrations replay the schema into isolated drill databases.
	Migrations []migrate.Pair
}

// VerifyControlPlane re-runs integrity verification for one stored backup and
// updates verify_state (kriter 2: verification state is always reported and
// re-checkable).
func (s *Service) VerifyControlPlane(ctx context.Context, backupID string) (Record, error) {
	if err := ValidateBackupID(backupID); err != nil {
		return Record{}, err
	}
	rec, err := s.Store.GetBackup(ctx, backupID)
	if err != nil {
		return Record{}, err
	}
	if rec.Scope != ScopeControlPlane {
		return Record{}, ErrInvalidState
	}
	if rec.StoragePath == "" {
		return Record{}, ErrCorruptBackup
	}
	vr, err := s.verifyFile(ctx, rec.StoragePath)
	if err != nil {
		return s.Store.UpdateVerify(ctx, backupID, VerifyFailed, truncate(err.Error(), 500))
	}
	return s.Store.UpdateVerify(ctx, backupID, VerifyVerified, vr.summary)
}

// ListBackups returns records (scope filter: "", "control_plane" or "node").
func (s *Service) ListBackups(ctx context.Context, scope, nodeID string, limit int) ([]Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.Store.ListBackups(ctx, scope, nodeID, limit)
}

// ListDrills returns recent restore drills (nodeID "" = all).
func (s *Service) ListDrills(ctx context.Context, nodeID string, limit int) ([]Drill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.Store.ListDrills(ctx, nodeID, limit)
}

// ---- Faz 14 task-engine contract (called from internal/tasks registry) ----

// DumpCP runs the Control Plane backup and returns the typed result map for
// the backup.cp.run task operation.
func (s *Service) DumpCP(ctx context.Context, pool *pgxpool.Pool) (map[string]any, error) {
	rec, err := s.Dump(ctx, pool)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"backup_id": rec.ID, "status": rec.Status, "verify_state": rec.VerifyState,
		"size_bytes": rec.SizeBytes, "sha256": rec.SHA256, "verify_detail": rec.VerifyDetail,
	}, nil
}

// VerifyCP re-verifies one stored backup and returns the typed result map.
func (s *Service) VerifyCP(ctx context.Context, backupID string) (map[string]any, error) {
	rec, err := s.VerifyControlPlane(ctx, backupID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"backup_id": rec.ID, "verify_state": rec.VerifyState, "verify_detail": rec.VerifyDetail,
	}, nil
}

// DrillCP rehearses an isolated restore and returns the typed result map.
func (s *Service) DrillCP(ctx context.Context, pool *pgxpool.Pool, backupID, actor string) (map[string]any, error) {
	d, err := s.RestoreDrill(ctx, pool, backupID, actor)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"drill_id": d.ID, "status": d.Status, "target": d.Target,
		"tables_restored": d.TablesRestored, "rows_restored": d.RowsRestored,
		"detail": d.Detail,
	}, nil
}

// ---- Node backup result recorder (agentsrv hook, primitive signature —
// import-cycle free) ----

// NodeSink records node backup task outcomes (scope=node rows + drills) when
// backup.* tasks complete. Nil-safe wiring: agentsrv skips when absent.
type NodeSink struct {
	Store Store
}

// RecordNodeTask persists one completed node backup task result. backup.run /
// backup.verify update backup records; backup.restore records a drill. The
// nodeID comes from the task itself — the sink NEVER trusts result fields for
// scoping (kriter 1: node backups are node-scoped, structurally).
func (ns *NodeSink) RecordNodeTask(ctx context.Context, operation, nodeID, actor, taskID string, result map[string]any) error {
	if ns == nil || ns.Store == nil || nodeID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var p struct {
		BackupID     string `json:"backup_id"`
		SnapshotID   string `json:"snapshot_id"`
		SHA256       string `json:"sha256"`
		SizeBytes    int64  `json:"size_bytes"`
		VerifyState  string `json:"verify_state"`
		VerifyDetail string `json:"verify_detail"`
		Target       string `json:"target"`
		Detail       string `json:"detail"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("backup sink: sonuç şeması uyumsuz: %w", err)
	}

	switch operation {
	case "backup.run":
		rec := &Record{
			Scope:  ScopeNode,
			NodeID: nodeID,
			Kind:   "restic",
			Status: StatusSuccess,
			TaskID: taskID,
		}
		id, err := ns.Store.InsertBackup(ctx, rec)
		if err != nil {
			return err
		}
		vs := p.VerifyState
		if vs != VerifyVerified && vs != VerifyFailed {
			vs = VerifyUnverified
		}
		_, err = ns.Store.CompleteBackup(ctx, id, StatusSuccess, p.SizeBytes, p.SHA256, p.SnapshotID,
			"", truncate(p.VerifyDetail, 500))
		if err == nil && vs != VerifyVerified {
			// Node-reported honesty: an absent/failed node-side integrity
			// check must never show as verified.
			_, err = ns.Store.UpdateVerify(ctx, id, vs, truncate(p.VerifyDetail, 500))
		}
		return err
	case "backup.verify":
		if p.BackupID == "" {
			return nil
		}
		if err := ValidateBackupID(p.BackupID); err != nil {
			return nil
		}
		rec, err := ns.Store.GetBackup(ctx, p.BackupID)
		if err != nil {
			return err
		}
		if rec.NodeID != nodeID {
			return fmt.Errorf("backup sink: doğrulama yedeği başka node'a ait")
		}
		vs := p.VerifyState
		if vs != VerifyVerified && vs != VerifyFailed {
			vs = VerifyUnverified
		}
		_, err = ns.Store.UpdateVerify(ctx, p.BackupID, vs, truncate(p.VerifyDetail, 500))
		return err
	case "backup.restore":
		if p.BackupID == "" {
			return nil
		}
		if err := ValidateBackupID(p.BackupID); err != nil {
			return nil
		}
		rec, err := ns.Store.GetBackup(ctx, p.BackupID)
		if err != nil {
			return err
		}
		if rec.NodeID != nodeID {
			return fmt.Errorf("backup sink: restore yedeği başka node'a ait")
		}
		d, err := ns.Store.InsertDrill(ctx, p.BackupID, nodeID, truncate(p.Target, 250), actor, taskID)
		if err != nil {
			return err
		}
		_, err = ns.Store.CompleteDrill(ctx, d.ID, StatusSuccess, truncate(p.Detail, 1000), 0, 0)
		return err
	default:
		return nil
	}
}
