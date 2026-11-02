package httpapi

import (
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/cloud"
)

// CloudProviderCreate is the registration request. Credential values are
// write-only: they are sealed on arrival and never returned by any endpoint.
type CloudProviderCreate struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Endpoint    string            `json:"endpoint"`
	Region      string            `json:"region,omitempty"`
	Credentials map[string]string `json:"credentials"`
}

// handleListCloudProviders lists registrations (credential key names only).
// GET /api/v1/cloud-providers
func (a *api) handleListCloudProviders(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	rows, err := a.cloud.List(r.Context())
	if err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": rows})
}

// handleCreateCloudProvider registers a provider (audited; secrets sealed).
// POST /api/v1/cloud-providers
func (a *api) handleCreateCloudProvider(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	var body CloudProviderCreate
	if err := decodeJSON(r, &body); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	row, err := a.cloud.Create(r.Context(), cloud.CreateInput{
		Name:        body.Name,
		Kind:        cloud.Kind(body.Kind),
		Endpoint:    body.Endpoint,
		Region:      body.Region,
		Credentials: body.Credentials,
	})
	if err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	a.recordAudit(r, "cloud.provider.create", "cloud_provider", row.ID, map[string]any{
		"name": row.Name, "kind": string(row.Kind), "endpoint": row.Endpoint,
		"credential_keys": row.CredKeyNames,
	})
	writeJSON(w, http.StatusCreated, row)
}

// handleDeleteCloudProvider removes a registration (audited).
// DELETE /api/v1/cloud-providers/{providerID}
func (a *api) handleDeleteCloudProvider(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "providerID")
	if err := a.cloud.DeleteProvider(r.Context(), id); err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	a.recordAudit(r, "cloud.provider.delete", "cloud_provider", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleVerifyCloudProvider probes the credential and returns the honest
// permission matrix + probe report (audited).
// POST /api/v1/cloud-providers/{providerID}/verify
func (a *api) handleVerifyCloudProvider(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "providerID")
	report, err := a.cloud.VerifyCredentials(r.Context(), id)
	if err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	ok := true
	for _, pr := range report.Probes {
		if !pr.OK {
			ok = false
			break
		}
	}
	result := "success"
	if !ok {
		result = "failure"
	}
	a.recordAudit(r, "cloud.provider.verify", "cloud_provider", id, map[string]any{
		"result": result, "provider": string(report.Provider),
	})
	writeJSON(w, http.StatusOK, report)
}

// handleListCloudResources lists provider resources (audited, read-only).
// GET /api/v1/cloud-providers/{providerID}/resources?limit=
func (a *api) handleListCloudResources(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "providerID")
	res, err := a.cloud.ListResources(r.Context(), id, parseLimit(r, 100))
	if err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	a.recordAudit(r, "cloud.resource.list", "cloud_provider", id, map[string]any{"count": len(res)})
	writeJSON(w, http.StatusOK, map[string]any{"resources": res})
}

// handleDeleteCloudResource destroys one provider resource. The confirmation
// gate lives in the service layer (typed ErrConfirmationRequired without
// confirm) — the UI cannot bypass it via raw API calls (kriter 5).
// DELETE /api/v1/cloud-providers/{providerID}/resources/{resourceID}?confirm=true
func (a *api) handleDeleteCloudResource(w http.ResponseWriter, r *http.Request) {
	if a.cloud == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Bulut modülü yapılandırılmamış.", false)
		return
	}
	id := chiURLParam(r, "providerID")
	name := chiURLParam(r, "resourceID")
	confirm := r.URL.Query().Get("confirm") == "true"
	if !confirm {
		// Body fallback for clients that pass JSON instead of query params.
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if err := decodeJSON(r, &body); err == nil {
			confirm = body.Confirm
		}
	}
	if err := a.cloud.DeleteResource(r.Context(), id, name, confirm); err != nil {
		a.writeCloudError(w, r, err)
		return
	}
	a.recordAudit(r, "cloud.resource.delete", "cloud_resource", name, map[string]any{
		"provider_id": id, "confirmed": true,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "resource": name})
}

// writeCloudError maps cloud package errors to stable API responses.
func (a *api) writeCloudError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cloud.ErrProviderNotFound):
		a.writeError(w, r, http.StatusNotFound, "cloud_provider_not_found", "Sağlayıcı bulunamadı.", false)
	case errors.Is(err, cloud.ErrConfirmationRequired):
		a.writeError(w, r, http.StatusBadRequest, "confirmation_required", "Bu silme işlemi açık onay gerektirir (confirm=true).", false)
	case errors.Is(err, cloud.ErrResourceNotEmpty):
		a.writeError(w, r, http.StatusConflict, "resource_not_empty", "Kaynak boş değil; panel zorla silmez.", false)
	case errors.Is(err, cloud.ErrResourceNotFound):
		a.writeError(w, r, http.StatusNotFound, "resource_not_found", "Kaynak sağlayıcıda bulunamadı.", false)
	case errors.Is(err, cloud.ErrPermission):
		a.writeError(w, r, http.StatusForbidden, "provider_permission_denied", "Kimlik bilgisi bu işlem için yetkili değil.", false)
	case errors.Is(err, cloud.ErrKeyNotConfigured):
		a.writeError(w, r, http.StatusServiceUnavailable, "secrets_key_not_configured", "CP_SECRETS_KEY yapılandırılmamış; kimlik bilgileri kullanılamaz.", false)
	case errors.Is(err, cloud.ErrProviderLimit):
		a.writeError(w, r, http.StatusConflict, "provider_limit", err.Error(), false)
	case errors.Is(err, cloud.ErrUnknownKind),
		errors.Is(err, cloud.ErrInvalidName),
		errors.Is(err, cloud.ErrInvalidEndpoint),
		errors.Is(err, cloud.ErrCredIncomplete),
		errors.Is(err, cloud.ErrResourceNameInvalid):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	case errors.Is(err, cloud.ErrProviderUnreachable):
		a.writeError(w, r, http.StatusBadGateway, "provider_unreachable", err.Error(), true)
	default:
		a.logger.Error("cloud_request_failed",
			"error", err.Error(),
			"request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Beklenmeyen sunucu hatası.", false)
	}
}
