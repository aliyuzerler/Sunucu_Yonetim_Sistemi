package inventory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore builds the SQL-backed inventory store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// CreateNode inserts a node and its tags in one transaction.
func (s *PGStore) CreateNode(ctx context.Context, n *Node) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("node transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
INSERT INTO nodes (display_name, hostname, os_family, os_name, os_version, arch, status)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at, updated_at`,
		n.DisplayName, n.Hostname, n.OSFamily, n.OSName, n.OSVersion, n.Arch, n.Status).
		Scan(&n.ID, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return fmt.Errorf("node oluşturulamadı: %w", err)
	}
	for _, t := range n.Tags {
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_tags (node_id, tag) VALUES ($1, $2)`, n.ID, t); err != nil {
			return fmt.Errorf("etiket yazılamadı: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("node commit: %w", err)
	}
	return nil
}

const nodeSelect = `
SELECT n.id, n.display_name, n.hostname, n.os_family, n.os_name, n.os_version,
       n.arch, n.agent_version, n.status, n.created_at, n.updated_at, n.last_seen_at,
       COALESCE((SELECT array_agg(t.tag ORDER BY t.tag) FROM node_tags t WHERE t.node_id = n.id), '{}'),
       COALESCE((SELECT array_agg(m.group_id::text ORDER BY m.group_id) FROM node_group_members m WHERE m.node_id = n.id), '{}')
FROM nodes n`

// GetNode fetches one node with tags and groups.
func (s *PGStore) GetNode(ctx context.Context, id string) (*Node, error) {
	n := &Node{}
	err := s.pool.QueryRow(ctx, nodeSelect+` WHERE n.id = $1`, id).Scan(
		&n.ID, &n.DisplayName, &n.Hostname, &n.OSFamily, &n.OSName, &n.OSVersion,
		&n.Arch, &n.AgentVersion, &n.Status, &n.CreatedAt, &n.UpdatedAt, &n.LastSeenAt,
		&n.Tags, &n.GroupIDs)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("node sorgusu: %w", err)
	}
	return n, nil
}

// ListNodes applies allowlisted filters with keyset pagination.
func (s *PGStore) ListNodes(ctx context.Context, f ListFilter) (NodePage, error) {
	where := []string{"true"}
	args := []any{}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("n.status = $%d", len(args)))
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM node_tags t WHERE t.node_id = n.id AND t.tag = $%d)", len(args)))
	}
	if f.Cursor != "" {
		ts, id, err := decodeNodeCursor(f.Cursor)
		if err != nil {
			return NodePage{}, fmt.Errorf("geçersiz imleç: %w", err)
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(n.created_at, n.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)

	q := nodeSelect + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY n.created_at DESC, n.id DESC LIMIT $" + fmt.Sprint(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return NodePage{}, fmt.Errorf("node listesi: %w", err)
	}
	defer rows.Close()

	page := NodePage{Nodes: []Node{}}
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.DisplayName, &n.Hostname, &n.OSFamily, &n.OSName,
			&n.OSVersion, &n.Arch, &n.AgentVersion, &n.Status, &n.CreatedAt, &n.UpdatedAt,
			&n.LastSeenAt, &n.Tags, &n.GroupIDs); err != nil {
			return NodePage{}, fmt.Errorf("node satırı: %w", err)
		}
		page.Nodes = append(page.Nodes, n)
	}
	if err := rows.Err(); err != nil {
		return NodePage{}, fmt.Errorf("node listesi hatası: %w", err)
	}
	if len(page.Nodes) > f.Limit {
		page.HasMore = true
		page.Nodes = page.Nodes[:f.Limit]
		last := page.Nodes[len(page.Nodes)-1]
		page.NextCursor = encodeNodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// UpdateNode persists mutable fields.
func (s *PGStore) UpdateNode(ctx context.Context, n *Node) error {
	ct, err := s.pool.Exec(ctx, `
UPDATE nodes SET display_name = $2, status = $3, updated_at = now()
WHERE id = $1`, n.ID, n.DisplayName, n.Status)
	if err != nil {
		return fmt.Errorf("node güncellenemedi: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTags replaces the tag set in one transaction.
func (s *PGStore) SetTags(ctx context.Context, nodeID string, tags []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tag transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM node_tags WHERE node_id = $1`, nodeID); err != nil {
		return fmt.Errorf("etiket temizliği: %w", err)
	}
	for _, t := range tags {
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_tags (node_id, tag) VALUES ($1, $2)`, nodeID, t); err != nil {
			return fmt.Errorf("etiket yazımı: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET updated_at = now() WHERE id = $1`, nodeID); err != nil {
		return fmt.Errorf("node güncelleme: %w", err)
	}
	return tx.Commit(ctx)
}

// ReplaceGroupMemberships sets group membership to exactly the given set.
func (s *PGStore) ReplaceGroupMemberships(ctx context.Context, nodeID string, groupIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("grup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM node_group_members WHERE node_id = $1`, nodeID); err != nil {
		return fmt.Errorf("grup temizliği: %w", err)
	}
	for _, g := range groupIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_group_members (node_id, group_id) VALUES ($1, $2)`, nodeID, g); err != nil {
			return fmt.Errorf("grup üyeliği: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// SetFacts upserts versioned OS facts metadata.
func (s *PGStore) SetFacts(ctx context.Context, nodeID string, facts map[string]any, observedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO node_facts (node_id, facts, facts_version, observed_at)
VALUES ($1, $2, 1, $3)
ON CONFLICT (node_id) DO UPDATE
SET facts = EXCLUDED.facts,
    facts_version = node_facts.facts_version + 1,
    observed_at = EXCLUDED.observed_at`, nodeID, facts, observedAt)
	if err != nil {
		return fmt.Errorf("facts yazımı: %w", err)
	}
	return nil
}

// UpsertCapability refreshes one capability row.
func (s *PGStore) UpsertCapability(ctx context.Context, c *Capability) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO node_capabilities (node_id, capability_id, version, available,
                               reason_unavailable, adapter_id, adapter_version, observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (node_id, capability_id) DO UPDATE
SET version = EXCLUDED.version,
    available = EXCLUDED.available,
    reason_unavailable = EXCLUDED.reason_unavailable,
    adapter_id = EXCLUDED.adapter_id,
    adapter_version = EXCLUDED.adapter_version,
    observed_at = EXCLUDED.observed_at`,
		c.NodeID, c.CapabilityID, c.Version, c.Available,
		c.ReasonUnavailable, c.AdapterID, c.AdapterVersion, c.ObservedAt)
	if err != nil {
		return fmt.Errorf("capability yazımı: %w", err)
	}
	return nil
}

// ListCapabilities returns all capability rows of a node.
func (s *PGStore) ListCapabilities(ctx context.Context, nodeID string) ([]Capability, error) {
	rows, err := s.pool.Query(ctx, `
SELECT node_id, capability_id, version, available, reason_unavailable,
       adapter_id, adapter_version, observed_at
FROM node_capabilities WHERE node_id = $1 ORDER BY capability_id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("capability listesi: %w", err)
	}
	defer rows.Close()
	var out []Capability
	for rows.Next() {
		var c Capability
		if err := rows.Scan(&c.NodeID, &c.CapabilityID, &c.Version, &c.Available,
			&c.ReasonUnavailable, &c.AdapterID, &c.AdapterVersion, &c.ObservedAt); err != nil {
			return nil, fmt.Errorf("capability satırı: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateGroup inserts a node group.
func (s *PGStore) CreateGroup(ctx context.Context, g *Group) error {
	err := s.pool.QueryRow(ctx, `
INSERT INTO node_groups (name, description) VALUES ($1, $2)
RETURNING id, created_at`, g.Name, g.Description).Scan(&g.ID, &g.CreatedAt)
	if err != nil {
		return fmt.Errorf("grup oluşturulamadı: %w", err)
	}
	return nil
}

// GetGroup fetches one group.
func (s *PGStore) GetGroup(ctx context.Context, id string) (*Group, error) {
	g := &Group{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, description, created_at FROM node_groups WHERE id = $1`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("grup sorgusu: %w", err)
	}
	return g, nil
}

// ListGroups returns all groups ordered by name.
func (s *PGStore) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, created_at FROM node_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("grup listesi: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("grup satırı: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// NodeGroupIDs returns group memberships of a node.
func (s *PGStore) NodeGroupIDs(ctx context.Context, nodeID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT group_id::text FROM node_group_members WHERE node_id = $1`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("grup üyelikleri: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountNodes returns total node count.
func (s *PGStore) CountNodes(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&n); err != nil {
		return 0, fmt.Errorf("node sayımı: %w", err)
	}
	return n, nil
}
