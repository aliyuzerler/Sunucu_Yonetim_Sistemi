// Package inventory implements the node inventory domain service: nodes,
// groups, tags, OS facts metadata and capability records (docs/phases/phase-02.md).
package inventory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/audit"
)

// Node statuses (Faz 02): agents arrive in Faz 03, so nodes start 'pending'.
const (
	StatusPending = "pending"
	StatusActive  = "active"
	StatusOffline = "offline"
	StatusRetired = "retired"
)

// OS families.
const (
	OSLinux   = "linux"
	OSWindows = "windows"
	OSUnknown = "unknown"
)

var (
	tagRE      = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
	hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Validation errors; handlers map them to 400 responses.
var (
	ErrInvalidDisplayName = errors.New("görünen ad 1..128 karakter olmalı")
	ErrInvalidHostname    = errors.New("hostname biçimi geçersiz")
	ErrInvalidOSFamily    = errors.New("os_family linux|windows|unknown olmalı")
	ErrInvalidStatus      = errors.New("geçersiz node durumu")
	ErrInvalidTag         = errors.New("etiket biçimi geçersiz (a-z0-9_.:-, 1..64)")
	ErrTooManyTags        = errors.New("en fazla 32 etiket")
	ErrInvalidUUID        = errors.New("kimlik UUID olmalı")
	ErrFactsTooLarge      = errors.New("facts verisi çok büyük (maks 64KiB)")
	ErrFactsNotObject     = errors.New("facts bir JSON nesnesi olmalı")
	ErrNotFound           = errors.New("kayıt bulunamadı")
)

// Node is an inventory node.
type Node struct {
	ID           string     `json:"id"`
	DisplayName  string     `json:"display_name"`
	Hostname     *string    `json:"hostname,omitempty"`
	OSFamily     string     `json:"os_family"`
	OSName       *string    `json:"os_name,omitempty"`
	OSVersion    *string    `json:"os_version,omitempty"`
	Arch         *string    `json:"arch,omitempty"`
	AgentVersion *string    `json:"agent_version,omitempty"`
	Status       string     `json:"status"`
	Tags         []string   `json:"tags"`
	GroupIDs     []string   `json:"group_ids"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

// Group is a node group usable in RBAC scopes.
type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Capability mirrors the capability-system.md record (Faz 03 populates it
// from agent reports; the model and API exist from Faz 02).
type Capability struct {
	NodeID            string    `json:"node_id"`
	CapabilityID      string    `json:"capability_id"`
	Version           string    `json:"version"`
	Available         bool      `json:"available"`
	ReasonUnavailable *string   `json:"reason_unavailable,omitempty"`
	AdapterID         *string   `json:"adapter_id,omitempty"`
	AdapterVersion    *string   `json:"adapter_version,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
}

// CreateNodeInput is the validated request for node creation.
type CreateNodeInput struct {
	DisplayName string
	Hostname    *string
	OSFamily    string
	OSName      *string
	OSVersion   *string
	Arch        *string
	Tags        []string
	GroupIDs    []string
}

// ListFilter is an allowlisted listing filter with keyset cursor.
type ListFilter struct {
	Status string
	Tag    string
	Limit  int
	Cursor string
}

// NodePage is one page of nodes.
type NodePage struct {
	Nodes      []Node `json:"nodes"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Store is the persistence contract for inventory.
type Store interface {
	CreateNode(ctx context.Context, n *Node) error
	GetNode(ctx context.Context, id string) (*Node, error)
	ListNodes(ctx context.Context, f ListFilter) (NodePage, error)
	UpdateNode(ctx context.Context, n *Node) error
	SetTags(ctx context.Context, nodeID string, tags []string) error
	ReplaceGroupMemberships(ctx context.Context, nodeID string, groupIDs []string) error
	SetFacts(ctx context.Context, nodeID string, facts map[string]any, observedAt time.Time) error
	UpsertCapability(ctx context.Context, c *Capability) error
	ListCapabilities(ctx context.Context, nodeID string) ([]Capability, error)
	CreateGroup(ctx context.Context, g *Group) error
	GetGroup(ctx context.Context, id string) (*Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	NodeGroupIDs(ctx context.Context, nodeID string) ([]string, error)
	CountNodes(ctx context.Context) (int64, error)
}

// Service implements inventory use cases with validation and audit.
type Service struct {
	store Store
	audit audit.Recorder
}

// NewService builds the inventory service.
func NewService(store Store, rec audit.Recorder) *Service {
	return &Service{store: store, audit: rec}
}

// Store exposes the persistence layer for narrow cross-module reads
// (RBAC node→group resolution). Mutations must go through Service methods.
func (s *Service) Store() Store { return s.store }

func validateTags(tags []string) error {
	if len(tags) > 32 {
		return ErrTooManyTags
	}
	for _, t := range tags {
		if !tagRE.MatchString(t) {
			return ErrInvalidTag
		}
	}
	return nil
}

func validateOSFamily(f string) error {
	switch f {
	case OSLinux, OSWindows, OSUnknown:
		return nil
	default:
		return ErrInvalidOSFamily
	}
}

func validateStatus(s string) error {
	switch s {
	case StatusPending, StatusActive, StatusOffline, StatusRetired:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func validateHostname(h *string) error {
	if h == nil || *h == "" {
		return nil
	}
	if !hostnameRE.MatchString(*h) {
		return ErrInvalidHostname
	}
	return nil
}

// CreateNode registers a node and writes a node.create audit event.
func (s *Service) CreateNode(ctx context.Context, actor, correlationID string, in CreateNodeInput) (*Node, error) {
	if in.DisplayName == "" || len(in.DisplayName) > 128 {
		return nil, ErrInvalidDisplayName
	}
	if in.OSFamily == "" {
		in.OSFamily = OSUnknown
	}
	if err := validateOSFamily(in.OSFamily); err != nil {
		return nil, err
	}
	if err := validateHostname(in.Hostname); err != nil {
		return nil, err
	}
	if err := validateTags(in.Tags); err != nil {
		return nil, err
	}
	for _, g := range in.GroupIDs {
		if !uuidRE.MatchString(g) {
			return nil, ErrInvalidUUID
		}
	}

	n := &Node{
		DisplayName: in.DisplayName,
		Hostname:    in.Hostname,
		OSFamily:    in.OSFamily,
		OSName:      in.OSName,
		OSVersion:   in.OSVersion,
		Arch:        in.Arch,
		Status:      StatusPending,
		Tags:        dedupe(in.Tags),
	}
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if err := s.store.CreateNode(ctx, n); err != nil {
		return nil, err
	}
	if len(in.GroupIDs) > 0 {
		if err := s.store.ReplaceGroupMemberships(ctx, n.ID, in.GroupIDs); err != nil {
			return nil, err
		}
		n.GroupIDs = in.GroupIDs
	} else {
		n.GroupIDs = []string{}
	}

	s.record(ctx, actor, correlationID, "node.create", "node", n.ID, audit.ResultSuccess,
		map[string]any{"display_name": n.DisplayName, "os_family": n.OSFamily, "tags": n.Tags})
	return n, nil
}

// GetNode returns one node with tags and groups.
func (s *Service) GetNode(ctx context.Context, id string) (*Node, error) {
	if !uuidRE.MatchString(id) {
		return nil, ErrInvalidUUID
	}
	n, err := s.store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.GroupIDs == nil {
		n.GroupIDs = []string{}
	}
	return n, nil
}

// ListNodes lists nodes with allowlisted filters and keyset pagination.
func (s *Service) ListNodes(ctx context.Context, f ListFilter) (NodePage, error) {
	if f.Status != "" {
		if err := validateStatus(f.Status); err != nil {
			return NodePage{}, err
		}
	}
	if f.Tag != "" && !tagRE.MatchString(f.Tag) {
		return NodePage{}, ErrInvalidTag
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	page, err := s.store.ListNodes(ctx, f)
	if err != nil {
		return NodePage{}, err
	}
	for i := range page.Nodes {
		if page.Nodes[i].Tags == nil {
			page.Nodes[i].Tags = []string{}
		}
		if page.Nodes[i].GroupIDs == nil {
			page.Nodes[i].GroupIDs = []string{}
		}
	}
	return page, nil
}

// UpdateNodeInput carries mutable node fields; nil = unchanged.
type UpdateNodeInput struct {
	DisplayName *string
	Status      *string
}

// UpdateNode changes mutable fields and audits the change.
func (s *Service) UpdateNode(ctx context.Context, actor, correlationID, nodeID string, in UpdateNodeInput) (*Node, error) {
	if !uuidRE.MatchString(nodeID) {
		return nil, ErrInvalidUUID
	}
	n, err := s.store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if in.DisplayName != nil {
		if *in.DisplayName == "" || len(*in.DisplayName) > 128 {
			return nil, ErrInvalidDisplayName
		}
		n.DisplayName = *in.DisplayName
	}
	if in.Status != nil {
		if err := validateStatus(*in.Status); err != nil {
			return nil, err
		}
		n.Status = *in.Status
	}
	if err := s.store.UpdateNode(ctx, n); err != nil {
		return nil, err
	}
	s.record(ctx, actor, correlationID, "node.update", "node", n.ID, audit.ResultSuccess,
		map[string]any{"status": n.Status, "display_name": n.DisplayName})
	return n, nil
}

// SetTags replaces the tag set of a node.
func (s *Service) SetTags(ctx context.Context, actor, correlationID, nodeID string, tags []string) (*Node, error) {
	if !uuidRE.MatchString(nodeID) {
		return nil, ErrInvalidUUID
	}
	if err := validateTags(tags); err != nil {
		return nil, err
	}
	tags = dedupe(tags)
	if tags == nil {
		tags = []string{}
	}
	if err := s.store.SetTags(ctx, nodeID, tags); err != nil {
		return nil, err
	}
	s.record(ctx, actor, correlationID, "node.tag", "node", nodeID, audit.ResultSuccess,
		map[string]any{"tags": tags})
	return s.GetNode(ctx, nodeID)
}

// SetFacts stores versioned OS facts metadata for a node.
func (s *Service) SetFacts(ctx context.Context, actor, correlationID, nodeID string, facts map[string]any) error {
	if !uuidRE.MatchString(nodeID) {
		return ErrInvalidUUID
	}
	if facts == nil {
		return ErrFactsNotObject
	}
	b, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("facts serileştirilemedi: %w", err)
	}
	if len(b) > 64<<10 {
		return ErrFactsTooLarge
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		return err
	}
	if err := s.store.SetFacts(ctx, nodeID, facts, time.Now().UTC()); err != nil {
		return err
	}
	s.record(ctx, actor, correlationID, "node.facts", "node", nodeID, audit.ResultSuccess,
		map[string]any{"facts_bytes": len(b)})
	return nil
}

// UpsertCapability records or refreshes one node capability.
func (s *Service) UpsertCapability(ctx context.Context, actor, correlationID string, c *Capability) error {
	if !uuidRE.MatchString(c.NodeID) {
		return ErrInvalidUUID
	}
	if !capIDRE.MatchString(c.CapabilityID) || strings.TrimSpace(c.Version) == "" || len(c.Version) > 32 {
		return errors.New("capability kimliği/sürümü geçersiz")
	}
	if _, err := s.store.GetNode(ctx, c.NodeID); err != nil {
		return err
	}
	c.ObservedAt = time.Now().UTC()
	if err := s.store.UpsertCapability(ctx, c); err != nil {
		return err
	}
	s.record(ctx, actor, correlationID, "node.capability", "node", c.NodeID, audit.ResultSuccess,
		map[string]any{"capability_id": c.CapabilityID, "available": c.Available})
	return nil
}

// ListCapabilities returns capability rows of a node.
func (s *Service) ListCapabilities(ctx context.Context, nodeID string) ([]Capability, error) {
	if !uuidRE.MatchString(nodeID) {
		return nil, ErrInvalidUUID
	}
	caps, err := s.store.ListCapabilities(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if caps == nil {
		caps = []Capability{}
	}
	return caps, nil
}

// CreateGroup adds a node group (used by RBAC scopes).
func (s *Service) CreateGroup(ctx context.Context, actor, correlationID, name, description string) (*Group, error) {
	if !groupRE.MatchString(name) {
		return nil, errors.New("grup adı biçimi geçersiz (a-z0-9_.-, 1..64)")
	}
	if len(description) > 256 {
		return nil, errors.New("grup açıklaması çok uzun")
	}
	g := &Group{Name: name, Description: description}
	if err := s.store.CreateGroup(ctx, g); err != nil {
		return nil, err
	}
	s.record(ctx, actor, correlationID, "node.group.create", "node_group", g.ID, audit.ResultSuccess,
		map[string]any{"name": g.Name})
	return g, nil
}

// ListGroups returns all groups.
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []Group{}
	}
	return groups, nil
}

// AssignGroup sets a node's group memberships to exactly the given set.
func (s *Service) AssignGroup(ctx context.Context, actor, correlationID, nodeID string, groupIDs []string) (*Node, error) {
	if !uuidRE.MatchString(nodeID) {
		return nil, ErrInvalidUUID
	}
	for _, g := range groupIDs {
		if !uuidRE.MatchString(g) {
			return nil, ErrInvalidUUID
		}
		if _, err := s.store.GetGroup(ctx, g); err != nil {
			return nil, err
		}
	}
	if _, err := s.store.GetNode(ctx, nodeID); err != nil {
		return nil, err
	}
	if err := s.store.ReplaceGroupMemberships(ctx, nodeID, dedupe(groupIDs)); err != nil {
		return nil, err
	}
	s.record(ctx, actor, correlationID, "node.group.assign", "node", nodeID, audit.ResultSuccess,
		map[string]any{"group_ids": dedupe(groupIDs)})
	return s.GetNode(ctx, nodeID)
}

// CountNodes returns the total node count (overview card).
func (s *Service) CountNodes(ctx context.Context) (int64, error) {
	return s.store.CountNodes(ctx)
}

func (s *Service) record(ctx context.Context, actor, correlationID, action, targetType, targetID, result string, meta map[string]any) {
	if s.audit == nil {
		return
	}
	e := audit.Event{
		Actor:         actor,
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		Result:        result,
		CorrelationID: correlationID,
		Metadata:      audit.Redact(meta).(map[string]any),
	}
	// Audit yazımı başarısızlığı işlemi geri almaz; gözlemlenebilirlik
	// kaybı loglanır (bağlam üst katmanda).
	_ = s.audit.Record(ctx, e)
}

var capIDRE = regexp.MustCompile(`^[a-z0-9_.]{3,128}$`)
var groupRE = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// Cursor helpers shared with the SQL store (keyset: created_at|id).

func encodeNodeCursor(ts time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", ts.UnixNano(), id)))
}

func decodeNodeCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("cursor biçimi")
	}
	var ns int64
	if _, err := fmt.Sscanf(parts[0], "%d", &ns); err != nil {
		return time.Time{}, "", errors.New("cursor zaman")
	}
	return time.Unix(0, ns), parts[1], nil
}
