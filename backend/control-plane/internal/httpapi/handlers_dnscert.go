package httpapi

// Faz 09: DNS sağlayıcı kayıtları ve SSL sertifika durumu REST yüzeyi.
//
// Gizlilik sözleşmesi (kriter 5): kimlik bilgileri YALNIZ yazılır — hiçbir
// okuma yanıtı credential içeriği döndürmez (yalnız has_credentials +
// anahtar ADLARI). Sertifika kayıtları da özel anahtar içermez (asla
// saklanmaz); sertifika PEM'i bile listelerde gösterilmez.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certmgmt"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dns"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
	"github.com/go-chi/chi/v5"
)

// handleListDNSProviders: GET /api/v1/dns-providers (dns:read).
func (a *api) handleListDNSProviders(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "DNS servisi yapılandırılmamış.", false)
		return
	}
	rows, err := a.dns.ListProviders(r.Context())
	if err != nil {
		a.writeDNSError(w, r, err)
		return
	}
	if rows == nil {
		rows = []dns.ProviderRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": rows})
}

// handleCreateDNSProvider: POST /api/v1/dns-providers (dns:control).
func (a *api) handleCreateDNSProvider(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "DNS servisi yapılandırılmamış.", false)
		return
	}
	var req struct {
		Name        string          `json:"name"`
		Kind        string          `json:"kind"`
		Credentials json.RawMessage `json:"credentials"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	row, err := a.dns.CreateProvider(r.Context(), req.Name, req.Kind, req.Credentials)
	if err != nil {
		a.writeDNSError(w, r, err)
		return
	}
	info := authFromOrAnonymous(r.Context())
	_ = a.audit.Record(r.Context(), auditEvent(info, "dns.provider.create", "dns_provider", row.ID, audit.ResultSuccess, requestIDFrom(r.Context()), map[string]any{"kind": row.Kind}))
	writeJSON(w, http.StatusCreated, row)
}

// handleDeleteDNSProvider: DELETE /api/v1/dns-providers/{providerID} (dns:control).
func (a *api) handleDeleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "DNS servisi yapılandırılmamış.", false)
		return
	}
	id := chi.URLParam(r, "providerID")
	if !dns.ValidProviderID(id) {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "Sağlayıcı kimliği UUID olmalı.", false)
		return
	}
	if err := a.dns.DeleteProvider(r.Context(), id); err != nil {
		a.writeDNSError(w, r, err)
		return
	}
	info := authFromOrAnonymous(r.Context())
	_ = a.audit.Record(r.Context(), auditEvent(info, "dns.provider.delete", "dns_provider", id, audit.ResultSuccess, requestIDFrom(r.Context()), nil))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleListCertificates: GET /api/v1/certificates (cert:read).
func (a *api) handleListCertificates(w http.ResponseWriter, r *http.Request) {
	if a.certs == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Sertifika servisi yapılandırılmamış.", false)
		return
	}
	records, err := a.certs.List(r.Context())
	if err != nil {
		a.writeDNSError(w, r, err)
		return
	}
	if records == nil {
		records = []certmgmt.Record{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": records})
}

// handleGetCertificate: GET /api/v1/certificates/{certID} (cert:read).
func (a *api) handleGetCertificate(w http.ResponseWriter, r *http.Request) {
	if a.certs == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Sertifika servisi yapılandırılmamış.", false)
		return
	}
	id := chi.URLParam(r, "certID")
	rec, err := a.certs.Get(r.Context(), id)
	if err != nil {
		a.writeDNSError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// writeDNSError maps dns/certmgmt/secrets domain errors to API errors.
func (a *api) writeDNSError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, dns.ErrProviderNotFound), errors.Is(err, certmgmt.ErrCertNotFound):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Kayıt bulunamadı.", false)
	case errors.Is(err, dns.ErrKeyMissing), errors.Is(err, secrets.KeyNotFoundError{}):
		a.writeError(w, r, http.StatusServiceUnavailable, "secrets_not_configured",
			"Şifreleme anahtarı yapılandırılmadı (CP_SECRETS_KEY).", false)
	case errors.Is(err, dns.ErrKindUnknown), errors.Is(err, dns.ErrInvalidRecord),
		errors.Is(err, dns.ErrInvalidZone), errors.Is(err, dns.ErrTypeUnsupport),
		errors.Is(err, dns.ErrTTLBounds), errors.Is(err, dns.ErrLimit),
		errors.Is(err, dns.ErrConflict):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	default:
		a.logger.Error("dns_cert_domain_error", "error", err.Error(), "request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Beklenmeyen sunucu hatası.", false)
	}
}
