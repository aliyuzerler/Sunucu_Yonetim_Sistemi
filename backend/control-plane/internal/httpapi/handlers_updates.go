package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/updates"
)

// handleListUpdateArtifacts lists update artifact metadata.
// GET /api/v1/update-artifacts?target=
func (a *api) handleListUpdateArtifacts(w http.ResponseWriter, r *http.Request) {
	if a.updates == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Güncelleme modülü yapılandırılmamış.", false)
		return
	}
	rows, err := a.updates.List(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		a.writeUpdateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": rows, "trust_enabled": a.updates.TrustEnabled()})
}

// handlePublishUpdateArtifact stores a SIGNED update manifest (fail-closed:
// imza doğrulanamazsa yayın yok — ADR-0020). Audited.
// POST /api/v1/update-artifacts
func (a *api) handlePublishUpdateArtifact(w http.ResponseWriter, r *http.Request) {
	if a.updates == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Güncelleme modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := decodeJSON(r, &body); err != nil || len(body.Manifest) == 0 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "manifest alanı zorunlu.", false)
		return
	}
	row, err := a.updates.Publish(r.Context(), caller(r), body.Manifest)
	if err != nil {
		a.writeUpdateError(w, r, err)
		return
	}
	a.recordAudit(r, "update.artifact.publish", "update_artifact", row.ID, map[string]any{
		"target": row.Target, "version": row.Version, "sha256": row.SHA256,
	})
	writeJSON(w, http.StatusCreated, row)
}

// handleRevokeUpdateArtifact revokes an artifact (task creation refuses it).
// POST /api/v1/update-artifacts/{artifactID}/revoke — audited.
func (a *api) handleRevokeUpdateArtifact(w http.ResponseWriter, r *http.Request) {
	if a.updates == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Güncelleme modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "artifactID")
	if err := a.updates.Revoke(r.Context(), id); err != nil {
		a.writeUpdateError(w, r, err)
		return
	}
	a.recordAudit(r, "update.artifact.revoke", "update_artifact", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (a *api) writeUpdateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, updates.ErrNotFound):
		a.writeError(w, r, http.StatusNotFound, "update_artifact_not_found", "Update artifact bulunamadı.", false)
	case errors.Is(err, updates.ErrNoTrustKey):
		a.writeError(w, r, http.StatusServiceUnavailable, "trust_key_not_configured", "CP_UPDATE_TRUST_KEYS yapılandırılmamış; yayın kapalı.", false)
	case errors.Is(err, updates.ErrBadSignature):
		a.writeError(w, r, http.StatusBadRequest, "bad_signature", "Update manifest imzası doğrulanamadı; imzasız yayınlanamaz.", false)
	case errors.Is(err, updates.ErrDuplicate):
		a.writeError(w, r, http.StatusConflict, "duplicate_artifact", err.Error(), false)
	case errors.Is(err, updates.ErrRevoked):
		a.writeError(w, r, http.StatusConflict, "artifact_revoked", err.Error(), false)
	case errors.Is(err, updates.ErrUnknownTarget),
		errors.Is(err, updates.ErrBadManifest):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	default:
		a.writeError(w, r, http.StatusInternalServerError, "update_error", "Update işlemi başarısız.", false)
	}
}
