// Package rbac implements role-based access control with node/group scoping.
// The policy is DEFAULT DENY: access requires an explicit permission granted
// through a role binding whose scope covers the target resource
// (docs/architecture/security-boundaries.md).
package rbac

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Permission catalog (Faz 02). Names are stable API contract; new phases add
// new names, never repurpose existing ones.
const (
	PermUsersRead  = "users:read"
	PermUsersWrite = "users:write"
	PermNodesRead  = "nodes:read"
	PermNodesWrite = "nodes:write"
	PermTasksRead  = "tasks:read"
	PermTasksWrite = "tasks:write"
	PermAuditRead  = "audit:read"
)

// AllPermissions lists the seeded catalog in one place.
var AllPermissions = []string{
	PermUsersRead, PermUsersWrite,
	PermNodesRead, PermNodesWrite,
	PermTasksRead, PermTasksWrite,
	PermAuditRead,
}

// Scope types.
const (
	ScopeAll   = "all"
	ScopeGroup = "group"
	ScopeNode  = "node"
)

// Binding is one user→role grant with its resource scope.
type Binding struct {
	RoleName    string
	ScopeType   string
	ScopeID     string   // empty for ScopeAll
	Permissions []string // role permission names
}

// Resource identifies the target of an action. Zero value means a global
// (non-node-scoped) resource such as user management or audit listing.
type Resource struct {
	NodeID       string
	NodeGroupIDs []string // groups the node belongs to (resolved by caller)
}

// Decision explains the authorization outcome for audit trails.
type Decision struct {
	Allowed bool
	Reason  string
}

// Deny reasons (stable strings for logs/tests).
const (
	ReasonNoBindings      = "no_role_bindings"
	ReasonPermissionMiss  = "permission_not_granted"
	ReasonScopeMismatch   = "scope_mismatch"
	ReasonGlobalScopeNeed = "global_scope_required"
	ReasonAllowed         = "granted"
)

// Authorize is the pure default-deny policy: it returns allow only when a
// binding grants the permission AND its scope covers the resource.
func Authorize(bindings []Binding, perm string, res Resource) Decision {
	if len(bindings) == 0 {
		return Decision{Allowed: false, Reason: ReasonNoBindings}
	}
	for _, b := range bindings {
		if !containsPerm(b.Permissions, perm) {
			continue
		}
		switch b.ScopeType {
		case ScopeAll:
			return Decision{Allowed: true, Reason: ReasonAllowed}
		case ScopeNode:
			if res.NodeID != "" && b.ScopeID == res.NodeID {
				return Decision{Allowed: true, Reason: ReasonAllowed}
			}
		case ScopeGroup:
			if res.NodeID != "" && b.ScopeID != "" && containsStr(res.NodeGroupIDs, b.ScopeID) {
				return Decision{Allowed: true, Reason: ReasonAllowed}
			}
		}
	}
	// Distinguish denial causes for observability.
	for _, b := range bindings {
		if containsPerm(b.Permissions, perm) {
			if res.NodeID == "" && b.ScopeType != ScopeAll {
				return Decision{Allowed: false, Reason: ReasonGlobalScopeNeed}
			}
			return Decision{Allowed: false, Reason: ReasonScopeMismatch}
		}
	}
	return Decision{Allowed: false, Reason: ReasonPermissionMiss}
}

func containsPerm(perms []string, p string) bool {
	for _, x := range perms {
		if x == p {
			return true
		}
	}
	return false
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ValidatePermission ensures only cataloged permission names are accepted by
// API layer (defense against typos widening access).
func ValidatePermission(name string) error {
	for _, p := range AllPermissions {
		if p == name {
			return nil
		}
	}
	return fmt.Errorf("bilinmeyen izin adı: %q", name)
}

// Store supplies user bindings.
type Store interface {
	BindingsForUser(ctx context.Context, userID string) ([]Binding, error)
}

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed RBAC store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// BindingsForUser aggregates role bindings and their permission sets. Only
// active users resolve to bindings (disabled users deny everywhere).
func (s *PGStore) BindingsForUser(ctx context.Context, userID string) ([]Binding, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	const q = `
SELECT r.name, b.scope_type, COALESCE(b.scope_id::text, ''), array_agg(p.name ORDER BY p.name)
FROM user_role_bindings b
JOIN roles r ON r.id = b.role_id
JOIN users u ON u.id = b.user_id AND u.status = 'active'
JOIN role_permissions rp ON rp.role_id = b.role_id
JOIN permissions p ON p.id = rp.permission_id
WHERE b.user_id = $1
GROUP BY b.id, r.name, b.scope_type, b.scope_id`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac bağlamaları: %w", err)
	}
	defer rows.Close()

	var out []Binding
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.RoleName, &b.ScopeType, &b.ScopeID, &b.Permissions); err != nil {
			return nil, fmt.Errorf("rbac satırı: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rbac sorgu hatası: %w", err)
	}
	return out, nil
}

// Service performs authorization with store lookups.
type Service struct {
	store Store
}

// NewService builds the RBAC service.
func NewService(store Store) *Service { return &Service{store: store} }

// Check resolves bindings and applies the pure policy.
func (s *Service) Check(ctx context.Context, userID, permission string, res Resource) (Decision, error) {
	if err := ValidatePermission(permission); err != nil {
		return Decision{Allowed: false, Reason: "unknown_permission"}, err
	}
	bindings, err := s.store.BindingsForUser(ctx, userID)
	if err != nil {
		return Decision{}, err
	}
	return Authorize(bindings, permission, res), nil
}
