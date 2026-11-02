package httpapi

import (
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/backup"
)

// handleListBackups lists backup records (scope filter: control_plane|node).
// GET /api/v1/backups?scope=&node_id=&limit=
func (a *api) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if a.backup == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Yedekleme modülü yapılandırılmamış.", false)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != backup.ScopeControlPlane && scope != backup.ScopeNode {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "scope control_plane veya node olmalı.", false)
		return
	}
	recs, err := a.backup.ListBackups(r.Context(), scope, r.URL.Query().Get("node_id"), parseLimit(r, 50))
	if err != nil {
		a.writeBackupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": recs})
}

// handleListRestoreDrills lists restore drills.
// GET /api/v1/restore-drills?node_id=&limit=
func (a *api) handleListRestoreDrills(w http.ResponseWriter, r *http.Request) {
	if a.backup == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Yedekleme modülü yapılandırılmamış.", false)
		return
	}
	drills, err := a.backup.ListDrills(r.Context(), r.URL.Query().Get("node_id"), parseLimit(r, 50))
	if err != nil {
		a.writeBackupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drills": drills})
}

// parseLimit bounds an optional limit query parameter.
func parseLimit(r *http.Request, def int) int {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 || n > 200 {
		return def
	}
	return n
}

// writeBackupError maps backup package errors to stable API responses.
func (a *api) writeBackupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, backup.ErrBackupNotFound):
		a.writeError(w, r, http.StatusNotFound, "backup_not_found", "Yedek bulunamadı.", false)
	case errors.Is(err, backup.ErrInvalidBackupID), errors.Is(err, backup.ErrInvalidNodeScope):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, backup.ErrUnsafeTarget):
		a.writeError(w, r, http.StatusBadRequest, "unsafe_restore_target", err.Error(), false)
	case errors.Is(err, backup.ErrKeyInsideRepo), errors.Is(err, backup.ErrKeyMaterialInBackup):
		a.writeError(w, r, http.StatusConflict, "key_separation_violation", err.Error(), false)
	case errors.Is(err, backup.ErrDatabaseDenied):
		a.writeError(w, r, http.StatusForbidden, "restore_drill_denied", err.Error(), false)
	case errors.Is(err, backup.ErrCorruptBackup):
		a.writeError(w, r, http.StatusConflict, "backup_corrupt", err.Error(), false)
	case errors.Is(err, backup.ErrInvalidState):
		a.writeError(w, r, http.StatusConflict, "invalid_state", err.Error(), false)
	default:
		a.logger.Error("backup_request_failed",
			"error", err.Error(),
			"request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "backup_failed", "Yedekleme işlemi başarısız oldu.", false)
	}
}
