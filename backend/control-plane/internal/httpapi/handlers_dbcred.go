package httpapi

// Faz 10: db_credentials REST yüzeyi — veritabanı kullanıcı şifrelerinin
// tek-seferlik reveal yaşam döngüsü.
//
// Gizlilik sözleşmesi (kriter 2 — "password hiçbir zaman tekrar düz metin
// gösterilmemeli"):
//   - Liste (db:read) ASLA şifre/parmak-izi ötesi materyal döndürmez.
//   - Reveal (db:control) TEK SEFERLİKTİR: ikinci çağrı tipli 409.
//   - Her reveal audit kaydına işlenir; audit meta'sında şifre OLMAZ.

import (
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/dbcred"
	"github.com/go-chi/chi/v5"
)

// handleListDBCredentials: GET /api/v1/nodes/{nodeID}/db-credentials (db:read).
func (a *api) handleListDBCredentials(w http.ResponseWriter, r *http.Request) {
	if a.dbcreds == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "DB kimlik servisi yapılandırılmamış.", false)
		return
	}
	nodeID := chi.URLParam(r, "nodeID")
	if len(nodeID) != 36 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "Node kimliği UUID olmalı.", false)
		return
	}
	rows, err := a.dbcreds.List(r.Context(), nodeID)
	if err != nil {
		a.writeError(w, r, http.StatusInternalServerError, "internal", "Kimlik listesi alınamadı.", false)
		return
	}
	if rows == nil {
		rows = []dbcred.Credential{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": rows})
}

// handleRevealDBCredential: POST /api/v1/db-credentials/{credID}/reveal
// (db:control; CSRF korumalı mutasyon). Tek seferlik — ikinci çağrı 409.
func (a *api) handleRevealDBCredential(w http.ResponseWriter, r *http.Request) {
	if a.dbcreds == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "DB kimlik servisi yapılandırılmamış.", false)
		return
	}
	id := chi.URLParam(r, "credID")
	if len(id) != 36 {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "Kimlik UUID olmalı.", false)
		return
	}
	password, cred, err := a.dbcreds.Reveal(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, dbcred.ErrNotFound):
			a.writeError(w, r, http.StatusNotFound, "not_found", "Kimlik kaydı bulunamadı.", false)
		case errors.Is(err, dbcred.ErrAlreadyRevealed):
			a.writeError(w, r, http.StatusConflict, "already_revealed",
				"Şifre daha önce bir kez gösterildi; tekrar gösterilemez. Gerekirse kullanıcıyı yeniden oluşturun.", false)
		case errors.Is(err, dbcred.ErrKeyNotConfigured):
			a.writeError(w, r, http.StatusServiceUnavailable, "not_configured",
				"CP_SECRETS_KEY yapılandırılmamış — şifre kalıcı saklanmadı.", false)
		default:
			a.writeError(w, r, http.StatusInternalServerError, "internal", "Şifre çözülemedi.", false)
		}
		return
	}
	info := authFromOrAnonymous(r.Context())
	_ = a.audit.Record(r.Context(), auditEvent(info, "db.credential.reveal", "db_credential", cred.ID,
		audit.ResultSuccess, requestIDFrom(r.Context()), map[string]any{
			"node_id": cred.NodeID, "kind": cred.Kind, "username": cred.Username,
		}))
	// Response body: password exactly once. NEVER logged (writeJSON does not
	// log; the audit meta above carries no password).
	writeJSON(w, http.StatusOK, map[string]any{
		"credential":  cred,
		"password":    password,
		"one_time":    true,
		"fingerprint": cred.Fingerprint,
	})
}
