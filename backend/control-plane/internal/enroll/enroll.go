// Package enroll implements the agent enrollment flow (ADR-0008): admins
// issue single-use, short-lived tokens scoped to a node (identity takeover)
// or a group (new node creation); agents exchange token+CSR for a
// node-identity certificate. Token consumption, node creation/claiming and
// certificate recording happen in ONE transaction, so a token can never be
// used twice and the resulting certificate is always bound to exactly one
// immutable node ID.
package enroll

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/certs"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scope types.
const (
	ScopeNode  = "node"
	ScopeGroup = "group"
)

// ProtocolVersionV1 is the agent protocol version recorded at enrollment.
const ProtocolVersionV1 = "v1"

// Errors mapped by callers.
var (
	ErrTokenBad      = errors.New("kayıt token geçersiz")
	ErrTokenUsed     = errors.New("kayıt token zaten kullanılmış")
	ErrTokenExpired  = errors.New("kayıt token süresi dolmuş")
	ErrTokenRevoked  = errors.New("kayıt token iptal edilmiş")
	ErrScopeNotFound = errors.New("token kapsamındaki node/grup bulunamadı")
	ErrNodeRetired   = errors.New("emekli node için kayıt yapılamaz")
	ErrInvalidInput  = errors.New("kayıt isteği geçersiz")
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Token is the API view of an enrollment token (hashes never leave).
type Token struct {
	ID         string     `json:"id"`
	ScopeType  string     `json:"scope_type"`
	NodeID     *string    `json:"node_id,omitempty"`
	GroupID    *string    `json:"group_id,omitempty"`
	CreatedBy  string     `json:"created_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Service implements enrollment use cases on PostgreSQL.
type Service struct {
	pool *pgxpool.Pool
	CA   *certs.CA
}

// NewService builds the enrollment service.
func NewService(pool *pgxpool.Pool, ca *certs.CA) *Service {
	return &Service{pool: pool, CA: ca}
}

// IssueInput is the admin request to create a token.
type IssueInput struct {
	ScopeType string
	NodeID    string
	GroupID   string
	TTL       time.Duration
}

// IssueToken creates a single-use token and returns the plaintext exactly once.
func (s *Service) IssueToken(ctx context.Context, actor string, in IssueInput) (identity.Token, *Token, error) {
	if in.ScopeType != ScopeNode && in.ScopeType != ScopeGroup {
		return identity.Token{}, nil, ErrInvalidInput
	}
	if in.TTL <= 0 || in.TTL > time.Hour {
		in.TTL = 15 * time.Minute
	}
	switch in.ScopeType {
	case ScopeNode:
		if !uuidRE.MatchString(in.NodeID) {
			return identity.Token{}, nil, ErrInvalidInput
		}
	case ScopeGroup:
		if !uuidRE.MatchString(in.GroupID) {
			return identity.Token{}, nil, ErrInvalidInput
		}
	}

	// Kapsam hedefinin var olduğunu doğrula (node emekli olmamalı).
	var checkErr error
	switch in.ScopeType {
	case ScopeNode:
		var status string
		checkErr = s.pool.QueryRow(ctx,
			`SELECT status FROM nodes WHERE id = $1`, in.NodeID).Scan(&status)
		if checkErr == nil && status == "retired" {
			return identity.Token{}, nil, ErrNodeRetired
		}
	case ScopeGroup:
		var one int
		checkErr = s.pool.QueryRow(ctx,
			`SELECT 1 FROM node_groups WHERE id = $1`, in.GroupID).Scan(&one)
	}
	if errors.Is(checkErr, pgx.ErrNoRows) {
		return identity.Token{}, nil, ErrScopeNotFound
	}
	if checkErr != nil {
		return identity.Token{}, nil, fmt.Errorf("kapsam denetimi: %w", checkErr)
	}

	tok, err := identity.NewToken()
	if err != nil {
		return identity.Token{}, nil, err
	}
	t := &Token{
		ScopeType: in.ScopeType,
		CreatedBy: actor,
		ExpiresAt: time.Now().UTC().Add(in.TTL),
	}
	if in.ScopeType == ScopeNode {
		n := in.NodeID
		t.NodeID = &n
	} else {
		g := in.GroupID
		t.GroupID = &g
	}

	const q = `
INSERT INTO enrollment_tokens (token_hash, scope_type, node_id, group_id, created_by, expires_at)
VALUES ($1, $2, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid, $5, $6)
RETURNING id, created_at`
	err = s.pool.QueryRow(ctx, q, tok.Hash, in.ScopeType,
		in.NodeID, in.GroupID, actor, t.ExpiresAt).
		Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return identity.Token{}, nil, fmt.Errorf("token kaydı: %w", err)
	}
	return tok, t, nil
}

// EnrollResult is the outcome of a successful token consumption.
type EnrollResult struct {
	NodeID      string
	Certificate *certs.LeafCert
}

// NodeMeta is the agent self-description attached to an enrollment.
type NodeMeta struct {
	DisplayName  string
	Hostname     string
	OSFamily     string
	AgentVersion string
}

// Enroll validates the presented token, consumes it atomically and issues the
// node-identity certificate for the CSR. Node/group scope resolution and
// certificate recording share ONE transaction with token consumption.
func (s *Service) Enroll(ctx context.Context, tokenPlain, csrPEM string, meta NodeMeta) (*EnrollResult, error) {
	if s.CA == nil {
		return nil, certs.ErrCANotLoaded
	}
	if tokenPlain == "" || csrPEM == "" {
		return nil, ErrInvalidInput
	}
	if len(meta.AgentVersion) > 64 || len(meta.Hostname) > 253 || len(meta.DisplayName) > 128 {
		return nil, ErrInvalidInput
	}
	tokenHash := identity.HashToken(tokenPlain)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("enroll transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		tokenID    string
		scopeType  string
		nodeID     *string
		groupID    *string
		expiresAt  time.Time
		consumedAt *time.Time
		revokedAt  *time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT id, scope_type, node_id, group_id, expires_at, consumed_at, revoked_at
FROM enrollment_tokens WHERE token_hash = $1 FOR UPDATE`, tokenHash).
		Scan(&tokenID, &scopeType, &nodeID, &groupID, &expiresAt, &consumedAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenBad
	}
	if err != nil {
		return nil, fmt.Errorf("token sorgusu: %w", err)
	}
	switch {
	case revokedAt != nil:
		return nil, ErrTokenRevoked
	case consumedAt != nil:
		return nil, ErrTokenUsed
	case time.Now().After(expiresAt):
		return nil, ErrTokenExpired
	}

	resolvedNodeID := ""
	if scopeType == ScopeNode {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM nodes WHERE id = $1 FOR UPDATE`, *nodeID).
			Scan(&status); err != nil {
			return nil, ErrScopeNotFound
		}
		if status == "retired" {
			return nil, ErrNodeRetired
		}
		resolvedNodeID = *nodeID
		// Mevcut node devralınıyor: agent metadata'sını güncelle.
		if _, err := tx.Exec(ctx, `
UPDATE nodes SET agent_version = $2, protocol_version = $3,
                 hostname = COALESCE(NULLIF($4,''), hostname),
                 os_family = CASE WHEN $5 IN ('linux','windows','unknown') THEN $5 ELSE os_family END,
                 status = 'active', updated_at = now(), last_seen_at = now()
WHERE id = $1`, resolvedNodeID, meta.AgentVersion, ProtocolVersionV1, meta.Hostname, meta.OSFamily); err != nil {
			return nil, fmt.Errorf("node güncellenemedi: %w", err)
		}
	} else {
		// Group scope: yeni node oluştur ve gruba ekle.
		display := meta.DisplayName
		if display == "" {
			display = meta.Hostname
		}
		if display == "" {
			display = "agent-node"
		}
		err = tx.QueryRow(ctx, `
INSERT INTO nodes (display_name, hostname, os_family, agent_version, protocol_version, status, last_seen_at)
VALUES ($1, NULLIF($2,''), CASE WHEN $3 IN ('linux','windows','unknown') THEN $3 ELSE 'unknown' END, $4, $5, 'active', now())
RETURNING id`, display, meta.Hostname, meta.OSFamily, meta.AgentVersion, ProtocolVersionV1).Scan(&resolvedNodeID)
		if err != nil {
			return nil, fmt.Errorf("node oluşturulamadı: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO node_group_members (node_id, group_id) VALUES ($1, $2)`,
			resolvedNodeID, *groupID); err != nil {
			return nil, fmt.Errorf("grup üyeliği: %w", err)
		}
	}

	leaf, err := s.CA.IssueNodeCert([]byte(csrPEM), resolvedNodeID, 0)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO node_certificates (node_id, fingerprint_sha256, serial, not_before, not_after)
VALUES ($1, $2, $3, $4, $5)`,
		resolvedNodeID, leaf.Fingerprint, leaf.Serial, leaf.NotBefore, leaf.NotAfter); err != nil {
		return nil, fmt.Errorf("sertifika kaydı: %w", err)
	}

	ct, err := tx.Exec(ctx,
		`UPDATE enrollment_tokens SET consumed_at = now() WHERE id = $1 AND consumed_at IS NULL`,
		tokenID)
	if err != nil {
		return nil, fmt.Errorf("token tüketimi: %w", err)
	}
	if ct.RowsAffected() != 1 {
		return nil, ErrTokenUsed
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("enroll commit: %w", err)
	}
	return &EnrollResult{NodeID: resolvedNodeID, Certificate: leaf}, nil
}

// AuthorizeStreamCert verifies that the presented mTLS client certificate is
// the active certificate of an existing, non-retired node. TLS handshake has
// already verified the chain against the CA; this adds DB-level state
// (revocation, node existence) at stream setup and is cheap enough to call
// once per connection.
func (s *Service) AuthorizeStreamCert(ctx context.Context, nodeID, fingerprint string) error {
	var (
		status     string
		certStatus string
		notAfter   time.Time
	)
	err := s.pool.QueryRow(ctx, `
SELECT n.status, c.status, c.not_after
FROM node_certificates c JOIN nodes n ON n.id = c.node_id
WHERE c.fingerprint_sha256 = $1 AND n.id = $2::uuid`, fingerprint, nodeID).
		Scan(&status, &certStatus, &notAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTokenBad // sertifika kaydı yok → kimlik tanınmıyor
	}
	if err != nil {
		return fmt.Errorf("sertifika yetki sorgusu: %w", err)
	}
	if certStatus != "active" {
		return ErrTokenRevoked
	}
	if time.Now().After(notAfter) {
		return ErrTokenExpired
	}
	if status == "retired" {
		return ErrNodeRetired
	}
	return nil
}

// TouchHello records the agent hello (version/protocol metadata) and marks
// the node active with a fresh last_seen_at.
func (s *Service) TouchHello(ctx context.Context, nodeID, agentVersion, hostname, osFamily, osVersion, arch string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE nodes SET agent_version = NULLIF($2,''), protocol_version = $3,
                 hostname = COALESCE(NULLIF($4,''), hostname),
                 os_name = COALESCE(NULLIF($5,''), os_name),
                 os_version = COALESCE(NULLIF($6,''), os_version),
                 arch = COALESCE(NULLIF($7,''), arch),
                 status = 'active', last_seen_at = now(), updated_at = now()
WHERE id = $1::uuid`,
		nodeID, agentVersion, ProtocolVersionV1, hostname, "", osVersion, arch)
	if err != nil {
		return fmt.Errorf("hello güncellemesi: %w", err)
	}
	return nil
}

// TouchHeartbeat refreshes nodes.last_seen_at (and active status) only.
func (s *Service) TouchHeartbeat(ctx context.Context, nodeID string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE nodes SET last_seen_at = now(), status = 'active'
WHERE id = $1::uuid AND status <> 'retired'`, nodeID)
	if err != nil {
		return fmt.Errorf("heartbeat güncellemesi: %w", err)
	}
	return nil
}

// List returns all tokens, newest first (API view, no hashes).
func (s *Service) List(ctx context.Context) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, scope_type, node_id, group_id, created_by, expires_at, consumed_at, revoked_at, created_at
FROM enrollment_tokens ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("token listesi: %w", err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.ScopeType, &t.NodeID, &t.GroupID, &t.CreatedBy,
			&t.ExpiresAt, &t.ConsumedAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("token satırı: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke invalidates a pending token.
func (s *Service) Revoke(ctx context.Context, id string) error {
	ct, err := s.pool.Exec(ctx, `
UPDATE enrollment_tokens SET revoked_at = now()
WHERE id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("token iptali: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrTokenBad
	}
	return nil
}
