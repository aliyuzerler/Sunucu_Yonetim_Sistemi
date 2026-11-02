package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/rbac"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---- Faz 14: yedekleme/geri yükleme (ADR-0018) ----
//
// Control Plane yedeği (backup.cp.*) control_plane hedeflidir ve executor'ı
// backup.Service sözleşmesidir; node veri yedeği (backup.*) node hedeflidir
// ve broker ailesi --backup-enable ile opt-in'dir. İki kapsam ayrıdır
// (kriter 1): CP yedeği kendi PostgreSQL'inin COPY tabanlı mantıksal
// dökümüdür, node yedeği restic deposudur. Şemalar agent/internal/backupops
// ile AYNA şeklinde tutulur (iki taraf da doğrular).

// Faz14Backup is the Control Plane backup contract the registry needs
// (implemented by *backup.Service; faked in tests). Primitive types keep the
// import graph acyclic.
type Faz14Backup interface {
	DumpCP(ctx context.Context, pool *pgxpool.Pool) (map[string]any, error)
	VerifyCP(ctx context.Context, backupID string) (map[string]any, error)
	DrillCP(ctx context.Context, pool *pgxpool.Pool, backupID, actor string) (map[string]any, error)
}

// Faz14Services bundles the optional Faz 14 dependencies. A nil Backup means
// the control-plane backup operations are OMITTED from the registry (the
// deployment did not configure them); node backup operations are registry
// members regardless (agent-side gating applies).
type Faz14Services struct {
	Backup Faz14Backup
	Pool   *pgxpool.Pool
}

// CPBackupExecutor adapts the Faz14Backup contract to the Executor interface.
type CPBackupExecutor struct {
	Svc  Faz14Backup
	Pool *pgxpool.Pool
	Kind string // "run" | "verify" | "drill"
}

// Execute runs one control-plane backup operation.
func (e CPBackupExecutor) Execute(ctx context.Context, t *Task) (map[string]any, string, error) {
	if e.Svc == nil {
		return nil, "", errors.New("backup: kontrol paneli yedek servisi yapılandırılmamış")
	}
	switch e.Kind {
	case "run":
		res, err := e.Svc.DumpCP(ctx, e.Pool)
		return res, "control plane yedeği alındı", err
	case "verify":
		var p struct {
			BackupID string `json:"backup_id"`
		}
		if err := strictDecodeFaz14(t.Payload, &p); err != nil {
			return nil, "", err
		}
		if !uuidRE.MatchString(p.BackupID) {
			return nil, "", errors.New("backup_id geçerli bir UUID olmalı")
		}
		res, err := e.Svc.VerifyCP(ctx, p.BackupID)
		return res, "bütünlük doğrulaması tamamlandı", err
	case "drill":
		var p struct {
			BackupID string `json:"backup_id"`
		}
		if err := strictDecodeFaz14(t.Payload, &p); err != nil {
			return nil, "", err
		}
		if !uuidRE.MatchString(p.BackupID) {
			return nil, "", errors.New("backup_id geçerli bir UUID olmalı")
		}
		res, err := e.Svc.DrillCP(ctx, e.Pool, p.BackupID, t.Actor)
		return res, "izole restore tatbikatı tamamlandı", err
	default:
		return nil, "", fmt.Errorf("backup: bilinmeyen executor türü: %s", e.Kind)
	}
}

func strictDecodeFaz14(raw json.RawMessage, v any) error {
	if err := decodeStrict(raw, v); err != nil {
		return fmt.Errorf("payload şeması uyumsuz: %w", err)
	}
	return nil
}

// ---- Node payload validators (mirror of agent/internal/backupops) ----

// backupRepoInitPayload: no fields — the restic repository path is
// node-managed (fixed /var/backups/server-control/restic); the password file
// lives under /etc/server-control/backup (kriter 3 separation is structural).
type backupRepoInitPayload struct{}

// ValidateBackupRepoInit enforces the strictly empty schema: no fields, no
// unknown fields (the repo path is node-managed, never payload-supplied).
func ValidateBackupRepoInit(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var p map[string]json.RawMessage
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if len(p) != 0 {
		return errors.New("repo.init payload boş olmalı")
	}
	return nil
}

// backupRunPayload: optional path subset of the broker's configured backup
// sources. Free-form paths NEVER widen the surface: the agent intersects with
// --backup-source allowlist (defense at the execution boundary too).
type backupRunPayload struct {
	Paths []string `json:"paths,omitempty"`
}

// ValidateBackupRun mirrors agent/internal/backupops validation: ≤8 paths,
// each absolute, ≤512 chars, no "..", no control chars.
func ValidateBackupRun(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var p backupRunPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if len(p.Paths) > 8 {
		return errors.New("paths en fazla 8 yol içerebilir")
	}
	for _, path := range p.Paths {
		if err := validateBackupSourcePath(path); err != nil {
			return err
		}
	}
	return nil
}

// backupSnapshotListPayload: bounded list request.
type backupSnapshotListPayload struct {
	Limit int `json:"limit,omitempty"`
}

// ValidateBackupSnapshotList mirrors the agent schema (limit 0..100).
func ValidateBackupSnapshotList(payload json.RawMessage) error {
	if len(payload) == 0 {
		return nil
	}
	var p backupSnapshotListPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if p.Limit < 0 || p.Limit > 100 {
		return errors.New("limit 0..100 olmalı")
	}
	return nil
}

// backupVerifyPayload: CP record id + optional read-data depth.
type backupVerifyPayload struct {
	BackupID string `json:"backup_id"`
}

// ValidateBackupVerify: backup_id UUID zorunlu.
func ValidateBackupVerify(payload json.RawMessage) error {
	var p backupVerifyPayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !uuidRE.MatchString(p.BackupID) {
		return errors.New("backup_id geçerli bir UUID olmalı")
	}
	return nil
}

// backupRestorePayload: CP record id + restic snapshot id. The restore TARGET
// is chosen by the node (isolated dir under the managed restore root) — the
// payload carries no path at all.
type backupRestorePayload struct {
	BackupID string `json:"backup_id"`
	Snapshot string `json:"snapshot"`
}

var backupSnapshotRE = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// ValidateBackupRestore mirrors the agent schema.
func ValidateBackupRestore(payload json.RawMessage) error {
	var p backupRestorePayload
	if err := decodeStrict(payload, &p); err != nil {
		return fmt.Errorf("şema uyumsuz: %w", err)
	}
	if !uuidRE.MatchString(p.BackupID) {
		return errors.New("backup_id geçerli bir UUID olmalı")
	}
	if !backupSnapshotRE.MatchString(p.Snapshot) {
		return errors.New("snapshot geçersiz (8..64 onaltılık karakter)")
	}
	return nil
}

// validateBackupSourcePath is the shared shape check for run payload paths.
func validateBackupSourcePath(path string) error {
	if path == "" || len(path) > 512 {
		return errors.New("yol 1..512 karakter olmalı")
	}
	if !strings.HasPrefix(path, "/") {
		return errors.New("yol mutlak olmalı")
	}
	if strings.Contains(path, "..") {
		return errors.New("yol .. içeremez")
	}
	for _, r := range path {
		if r < 0x20 {
			return errors.New("yol kontrol karakteri içeremez")
		}
	}
	return nil
}

// buildFaz14Registry returns the Faz 14 operation specs.
func buildFaz14Registry(faz14 Faz14Services) []*OperationSpec {
	specs := []*OperationSpec{
		// Node veri yedeği (restic; broker ailesi --backup-enable ile
		// opt-in). Depo yolu node'da sabittir; parola dosyası ayrı kökte
		// tutulur (kriter 3) ve sonuç/audit yüzeylerine ASLA çıkmaz.
		{
			Name:         "backup.repo.init",
			Version:      "1.0.0",
			Class:        ClassIdempotent,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      2 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateBackupRepoInit,
			RequiredPerm: rbac.PermBackupCtrl,
		},
		{
			Name:         "backup.run",
			Version:      "1.0.0",
			Class:        ClassNonIdem,
			Target:       TargetNode,
			RetrySafe:    false,
			Timeout:      5 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateBackupRun,
			RequiredPerm: rbac.PermBackupCtrl,
		},
		{
			Name:         "backup.snapshot.list",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      60 * time.Second,
			MaxPayload:   1 << 10,
			Validate:     ValidateBackupSnapshotList,
			RequiredPerm: rbac.PermBackupRead,
		},
		{
			Name:         "backup.verify",
			Version:      "1.0.0",
			Class:        ClassReadOnly,
			Target:       TargetNode,
			RetrySafe:    true,
			Timeout:      5 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateBackupVerify,
			RequiredPerm: rbac.PermBackupCtrl,
		},
		{
			Name:         "backup.restore",
			Version:      "1.0.0",
			Class:        ClassNonIdem,
			Target:       TargetNode,
			RetrySafe:    false,
			Timeout:      5 * time.Minute,
			MaxPayload:   1 << 10,
			Validate:     ValidateBackupRestore,
			RequiredPerm: rbac.PermBackupCtrl,
		},
	}
	// Control Plane yedeği (kendi veritabanının mantıksal dökümü) yalnız
	// yedek servisi yapılandırıldığında kayda girer.
	if faz14.Backup != nil {
		specs = append(specs,
			&OperationSpec{
				Name:         "backup.cp.run",
				Version:      "1.0.0",
				Class:        ClassNonIdem,
				Target:       TargetControlPlane,
				RetrySafe:    false,
				Timeout:      5 * time.Minute,
				MaxPayload:   1 << 10,
				Validate:     ValidateNoopPayload,
				Exec:         CPBackupExecutor{Svc: faz14.Backup, Pool: faz14.Pool, Kind: "run"},
				RequiredPerm: rbac.PermBackupCtrl,
			},
			&OperationSpec{
				Name:         "backup.cp.verify",
				Version:      "1.0.0",
				Class:        ClassIdempotent,
				Target:       TargetControlPlane,
				RetrySafe:    true,
				Timeout:      2 * time.Minute,
				MaxPayload:   1 << 10,
				Validate:     ValidateBackupVerify,
				Exec:         CPBackupExecutor{Svc: faz14.Backup, Pool: faz14.Pool, Kind: "verify"},
				RequiredPerm: rbac.PermBackupCtrl,
			},
			&OperationSpec{
				Name:         "backup.cp.restore_drill",
				Version:      "1.0.0",
				Class:        ClassNonIdem,
				Target:       TargetControlPlane,
				RetrySafe:    false,
				Timeout:      5 * time.Minute,
				MaxPayload:   1 << 10,
				Validate:     ValidateBackupVerify,
				Exec:         CPBackupExecutor{Svc: faz14.Backup, Pool: faz14.Pool, Kind: "drill"},
				RequiredPerm: rbac.PermBackupCtrl,
			},
		)
	}
	return specs
}
