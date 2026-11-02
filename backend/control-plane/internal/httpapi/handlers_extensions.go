package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/extensions"
)

// handleListExtensions lists extension manifests + grants (kriter 1:
// istenen/onaylı izinler birebir görünür; varsayılan izin yoktur).
// GET /api/v1/extensions
func (a *api) handleListExtensions(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	rows, err := a.extensions.List(r.Context())
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"extensions": rows, "trust_enabled": a.extensions.TrustEnabled()})
}

// handleGetExtension returns one extension.
// GET /api/v1/extensions/{extensionID}
func (a *api) handleGetExtension(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	row, err := a.extensions.Get(r.Context(), chiURLParam(r, "extensionID"))
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// handleRegisterExtension registers a SIGNED manifest (fail-closed: imza
// doğrulanamazsa kayıt bile oluşmaz). Yeni sürüm, önceki tüm onayları geri
// alır — yeniden onay zorunludur (ADR-0019). Audited.
// POST /api/v1/extensions
func (a *api) handleRegisterExtension(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := decodeJSON(r, &body); err != nil || len(body.Manifest) == 0 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "manifest alanı zorunlu.", false)
		return
	}
	row, err := a.extensions.Register(r.Context(), caller(r), body.Manifest)
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	a.recordAudit(r, "extension.register", "extension", row.ID, map[string]any{
		"name": row.Name, "version": row.Version, "api_version": row.APIVersion,
		"state": row.State, "requested": len(row.Requested),
	})
	writeJSON(w, http.StatusCreated, row)
}

// handleApproveExtensionPermission grants ONE requested permission (audited).
// POST /api/v1/extensions/{extensionID}/permissions/approve
func (a *api) handleApproveExtensionPermission(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	if !body.Confirm {
		a.writeError(w, r, http.StatusBadRequest, "confirmation_required", "İzin onayı açık confirm gerektirir.", false)
		return
	}
	id := chiURLParam(r, "extensionID")
	row, err := a.extensions.ApprovePermission(r.Context(), id, body.Action)
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	a.recordAudit(r, "extension.permission.approve", "extension", id, map[string]any{
		"action": body.Action,
	})
	writeJSON(w, http.StatusOK, row)
}

// handleRevokeExtensionPermission withdraws ONE granted permission (audited).
// POST /api/v1/extensions/{extensionID}/permissions/revoke
func (a *api) handleRevokeExtensionPermission(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	id := chiURLParam(r, "extensionID")
	row, err := a.extensions.RevokePermission(r.Context(), id, body.Action)
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	a.recordAudit(r, "extension.permission.revoke", "extension", id, map[string]any{
		"action": body.Action,
	})
	writeJSON(w, http.StatusOK, row)
}

// handleRevokeExtension disables the extension entirely (audited).
// POST /api/v1/extensions/{extensionID}/revoke
func (a *api) handleRevokeExtension(w http.ResponseWriter, r *http.Request) {
	if a.extensions == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Eklenti modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "extensionID")
	row, err := a.extensions.Revoke(r.Context(), id)
	if err != nil {
		a.writeExtensionError(w, r, err)
		return
	}
	a.recordAudit(r, "extension.revoke", "extension", id, nil)
	writeJSON(w, http.StatusOK, row)
}

func (a *api) writeExtensionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, extensions.ErrNotFound):
		a.writeError(w, r, http.StatusNotFound, "extension_not_found", "Eklenti bulunamadı.", false)
	case errors.Is(err, extensions.ErrNoTrustKey):
		a.writeError(w, r, http.StatusServiceUnavailable, "trust_key_not_configured", "CP_EXTENSION_TRUST_KEYS yapılandırılmamış; eklenti kaydı kapalı.", false)
	case errors.Is(err, extensions.ErrBadSignature):
		a.writeError(w, r, http.StatusBadRequest, "bad_signature", "Manifest imzası güven kökleriyle doğrulanamadı.", false)
	case errors.Is(err, extensions.ErrAPIIncompatible):
		a.writeError(w, r, http.StatusBadRequest, "api_incompatible", err.Error(), false)
	case errors.Is(err, extensions.ErrDuplicate):
		a.writeError(w, r, http.StatusConflict, "duplicate_extension", err.Error(), false)
	case errors.Is(err, extensions.ErrUnknownPermission),
		errors.Is(err, extensions.ErrUnknownOp),
		errors.Is(err, extensions.ErrPermNotRequested),
		errors.Is(err, extensions.ErrBadManifest):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, extensions.ErrInvalidState):
		a.writeError(w, r, http.StatusConflict, "invalid_state", err.Error(), false)
	default:
		a.writeError(w, r, http.StatusInternalServerError, "extension_error", "Eklenti işlemi başarısız.", false)
	}
}
