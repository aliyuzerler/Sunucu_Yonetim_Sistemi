package httpapi

import (
	"errors"
	"net/http"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/inventory"
)

// handleListNodes lists nodes with allowlisted filters.
// GET /api/v1/nodes?status=&tag=&limit=&cursor=
func (a *api) handleListNodes(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	q := r.URL.Query()
	page, err := a.inventory.ListNodes(r.Context(), inventory.ListFilter{
		Status: q.Get("status"),
		Tag:    q.Get("tag"),
		Limit:  parseIntDefault(q.Get("limit"), 50),
		Cursor: q.Get("cursor"),
	})
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGetNode returns one node.
// GET /api/v1/nodes/{nodeID}
func (a *api) handleGetNode(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	n, err := a.inventory.GetNode(r.Context(), chiURLParam(r, "nodeID"))
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleCreateNode registers a node manually (agent enrollment is Faz 03).
// POST /api/v1/nodes
func (a *api) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		DisplayName string   `json:"display_name"`
		Hostname    *string  `json:"hostname"`
		OSFamily    string   `json:"os_family"`
		OSName      *string  `json:"os_name"`
		OSVersion   *string  `json:"os_version"`
		Arch        *string  `json:"arch"`
		Tags        []string `json:"tags"`
		GroupIDs    []string `json:"group_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	n, err := a.inventory.CreateNode(r.Context(), caller(r), requestIDFrom(r.Context()), inventory.CreateNodeInput{
		DisplayName: req.DisplayName,
		Hostname:    req.Hostname,
		OSFamily:    req.OSFamily,
		OSName:      req.OSName,
		OSVersion:   req.OSVersion,
		Arch:        req.Arch,
		Tags:        req.Tags,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

// handleUpdateNode changes mutable fields.
// PATCH /api/v1/nodes/{nodeID}
func (a *api) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		DisplayName *string `json:"display_name"`
		Status      *string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	n, err := a.inventory.UpdateNode(r.Context(), caller(r), requestIDFrom(r.Context()), chiURLParam(r, "nodeID"),
		inventory.UpdateNodeInput{DisplayName: req.DisplayName, Status: req.Status})
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleSetTags replaces node tags.
// PUT /api/v1/nodes/{nodeID}/tags {tags: [...]}
func (a *api) handleSetTags(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	n, err := a.inventory.SetTags(r.Context(), caller(r), requestIDFrom(r.Context()), chiURLParam(r, "nodeID"), req.Tags)
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleAssignGroups replaces group memberships.
// PUT /api/v1/nodes/{nodeID}/groups {group_ids: [...]}
func (a *api) handleAssignGroups(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	n, err := a.inventory.AssignGroup(r.Context(), caller(r), requestIDFrom(r.Context()), chiURLParam(r, "nodeID"), req.GroupIDs)
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// handleSetFacts stores OS facts metadata.
// PUT /api/v1/nodes/{nodeID}/facts {facts: {...}}
func (a *api) handleSetFacts(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		Facts map[string]any `json:"facts"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	if err := a.inventory.SetFacts(r.Context(), caller(r), requestIDFrom(r.Context()), chiURLParam(r, "nodeID"), req.Facts); err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleListCapabilities lists node capabilities.
// GET /api/v1/nodes/{nodeID}/capabilities
func (a *api) handleListCapabilities(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	caps, err := a.inventory.ListCapabilities(r.Context(), chiURLParam(r, "nodeID"))
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": caps})
}

// handleUpsertCapability records a capability snapshot.
// POST /api/v1/nodes/{nodeID}/capabilities
func (a *api) handleUpsertCapability(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		CapabilityID      string  `json:"capability_id"`
		Version           string  `json:"version"`
		Available         bool    `json:"available"`
		ReasonUnavailable *string `json:"reason_unavailable"`
		AdapterID         *string `json:"adapter_id"`
		AdapterVersion    *string `json:"adapter_version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	cap := &inventory.Capability{
		NodeID:            chiURLParam(r, "nodeID"),
		CapabilityID:      req.CapabilityID,
		Version:           req.Version,
		Available:         req.Available,
		ReasonUnavailable: req.ReasonUnavailable,
		AdapterID:         req.AdapterID,
		AdapterVersion:    req.AdapterVersion,
	}
	if err := a.inventory.UpsertCapability(r.Context(), caller(r), requestIDFrom(r.Context()), cap); err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, cap)
}

// handleListGroups lists node groups.
// GET /api/v1/node-groups
func (a *api) handleListGroups(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	groups, err := a.inventory.ListGroups(r.Context())
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// handleCreateGroup creates a node group.
// POST /api/v1/node-groups {name, description}
func (a *api) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if a.inventory == nil {
		a.writeError(w, r, http.StatusServiceUnavailable, "not_configured", "Envanter yapılandırılmamış.", false)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", "İstek gövdesi geçersiz.", false)
		return
	}
	g, err := a.inventory.CreateGroup(r.Context(), caller(r), requestIDFrom(r.Context()), req.Name, req.Description)
	if err != nil {
		a.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// writeDomainError maps domain errors to stable API responses.
func (a *api) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, inventory.ErrNotFound), errors.Is(err, inventory.ErrInvalidUUID):
		a.writeError(w, r, http.StatusNotFound, "not_found", "Kayıt bulunamadı.", false)
	case errors.Is(err, inventory.ErrInvalidDisplayName),
		errors.Is(err, inventory.ErrInvalidHostname),
		errors.Is(err, inventory.ErrInvalidOSFamily),
		errors.Is(err, inventory.ErrInvalidStatus),
		errors.Is(err, inventory.ErrInvalidTag),
		errors.Is(err, inventory.ErrTooManyTags),
		errors.Is(err, inventory.ErrFactsTooLarge),
		errors.Is(err, inventory.ErrFactsNotObject):
		a.writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), false)
	default:
		a.logger.Error("domain_error",
			"error", err.Error(),
			"request_id", requestIDFrom(r.Context()))
		a.writeError(w, r, http.StatusInternalServerError, "internal_error", "Beklenmeyen sunucu hatası.", false)
	}
}
