package certmgmt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists certificate lifecycle rows in PostgreSQL.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// recordCols: NULL-olabilir kolonlar (referanslar + materyal + zamanlar)
// okumada COALESCE ile Go sıfır-değerlerine iner (CI-24/25 dersi: pgx NULL
// taramasını *string/*time.Time'a yapmaz; pending kayıtlarda not_after vs
// NULL'dür).
const recordCols = `id, name, sans, COALESCE(provider_id::text, '') AS provider_id,
	COALESCE(node_id::text, '') AS node_id, csr_pem, state,
	COALESCE(not_after, '0001-01-01 00:00:00+00'::timestamptz) AS not_after,
	COALESCE(fingerprint, '') AS fingerprint,
	COALESCE(last_error, '') AS last_error,
	COALESCE(last_error_class, '') AS last_error_class, retry_after_secs,
	COALESCE(issued_at, '0001-01-01 00:00:00+00'::timestamptz) AS issued_at,
	COALESCE(revoked_at, '0001-01-01 00:00:00+00'::timestamptz) AS revoked_at,
	created_at, updated_at`

func scanRecord(row pgx.Row) (Record, error) {
	var r Record
	var sansJSON []byte
	err := row.Scan(&r.ID, &r.Name, &sansJSON, &r.ProviderID, &r.NodeID, &r.CSRPEM,
		&r.State, &r.NotAfter, &r.Fingerprint, &r.LastError, &r.LastErrorClass,
		&r.RetryAfterSecs, &r.IssuedAt, &r.RevokedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrCertNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if len(sansJSON) > 0 {
		_ = json.Unmarshal(sansJSON, &r.SANs)
	}
	return r, nil
}

// Create inserts a pending certificate record (CSR = public material).
func (s *Store) Create(ctx context.Context, r Record) (Record, error) {
	if r.State == "" {
		r.State = StatePending
	}
	sansJSON, err := json.Marshal(r.SANs)
	if err != nil {
		return Record{}, err
	}
	// Boş referanslar NULL yazılır (UUID parse hatası değil).
	var providerID, nodeID *string
	if r.ProviderID != "" {
		providerID = &r.ProviderID
	}
	if r.NodeID != "" {
		nodeID = &r.NodeID
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO certificates (name, sans, provider_id, node_id, csr_pem, state)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+recordCols,
		r.Name, sansJSON, providerID, nodeID, r.CSRPEM, r.State)
	return scanRecord(row)
}

// Get returns one record.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+recordCols+` FROM certificates WHERE id = $1`, id)
	return scanRecord(row)
}

// List returns all records, newest first, with derived fields filled.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+recordCols+` FROM certificates ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmtWrap("liste", err)
	}
	defer rows.Close()
	var out []Record
	now := time.Now()
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		fillDerived(&r, now)
		out = append(out, r)
	}
	return out, rows.Err()
}

func fillDerived(r *Record, now time.Time) {
	r.EffectiveState = EffectiveStateOf(*r, now)
	r.ExpiresInDays = DaysLeft(r.NotAfter, now)
	if r.EffectiveState == StateIssued && r.ExpiresInDays <= int(ExpiringSoonHorizon.Hours()/24) {
		r.ExpiringSoon = true
	}
}

// MarkIssuing transitions pending/renewal_failed → issuing.
func (s *Store) MarkIssuing(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, last_error = NULL, last_error_class = NULL,
		       retry_after_secs = 0, updated_at = now()
		WHERE id = $1 AND state IN ($3, $4, $5)`,
		id, StateIssuing, StatePending, StateRenewalFail, StateExpired)
	if err != nil {
		return fmtWrap("issuing", err)
	}
	return requireAffected(tag, "issuing")
}

// MarkRenewing transitions issued/expired/renewal_failed → renewing
// (yenileme başarısızlığından sonra yeniden deneme meşrudur — CI-26).
func (s *Store) MarkRenewing(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, last_error = NULL, last_error_class = NULL,
		       retry_after_secs = 0, updated_at = now()
		WHERE id = $1 AND state IN ($3, $4, $5)`,
		id, StateRenewing, StateIssued, StateExpired, StateRenewalFail)
	if err != nil {
		return fmtWrap("renewing", err)
	}
	return requireAffected(tag, "renewing")
}

// MarkIssued stores sealed cert+chain PEM and flips state to issued.
func (s *Store) MarkIssued(ctx context.Context, id string, certEnc, chainEnc []byte, notAfter time.Time, fingerprint string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, cert_pem_enc = $3, chain_pem_enc = $4,
		       not_after = $5, fingerprint = $6, issued_at = now(), updated_at = now(),
		       last_error = NULL, last_error_class = NULL, retry_after_secs = 0
		WHERE id = $1`,
		id, StateIssued, certEnc, chainEnc, notAfter, fingerprint)
	if err != nil {
		return fmtWrap("issued", err)
	}
	return requireAffected(tag, "issued")
}

// MarkFailed records a classified failure WITHOUT touching issued material:
// the live certificate (and the node copy) is unaffected by definition —
// criterion 4's "başarısız yenileme canlı sertifikayı bozmamalı".
func (s *Store) MarkFailed(ctx context.Context, id, state string, class string, msg string, retryAfterSecs int) error {
	if state != StatePending && state != StateRenewalFail {
		return errors.New("certmgmt: izin verilmeyen başarısızlık durumu")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, last_error = $3, last_error_class = $4,
		       retry_after_secs = $5, updated_at = now()
		WHERE id = $1`,
		id, state, msg, class, retryAfterSecs)
	if err != nil {
		return fmtWrap("failed", err)
	}
	return requireAffected(tag, "failed")
}

// MarkRevoked flips an issued certificate to revoked (material retained
// for audit; install/uninstall on the node is a separate operator action).
func (s *Store) MarkRevoked(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, revoked_at = now(), updated_at = now()
		WHERE id = $1`, id, StateRevoked)
	if err != nil {
		return fmtWrap("revoked", err)
	}
	return requireAffected(tag, "revoked")
}

// MarkExpired persists watcher-detected expiry (issued → expired).
func (s *Store) MarkExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE certificates SET state = $2, updated_at = now()
		WHERE state = $3 AND not_after IS NOT NULL AND not_after < $1`, now, StateExpired, StateIssued)
	if err != nil {
		return 0, fmtWrap("expired", err)
	}
	return int(tag.RowsAffected()), nil
}

// GetCertMaterial returns the sealed PEM blobs for the install pipeline.
func (s *Store) GetCertMaterial(ctx context.Context, id string) (certEnc, chainEnc []byte, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT cert_pem_enc, chain_pem_enc FROM certificates WHERE id = $1`, id).
		Scan(&certEnc, &chainEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrCertNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	// pending/issuing/renewal_failed→başarısız aşamalarda materyal YOK:
	// NULL → tipli sinyal (sessiz boş blob değil).
	if certEnc == nil {
		return nil, nil, fmtWrap("materyal", ErrCertNotFound)
	}
	return certEnc, chainEnc, nil
}

// Watcher periodically persists expiry transitions. Interval is injectable
// for tests (CI runs it with 50ms; production with time.Hour).
type Watcher struct {
	Store    *Store
	Interval time.Duration
}

// Run blocks until ctx is cancelled.
func (w Watcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = w.Store.MarkExpired(ctx, time.Now())
		}
	}
}

func requireAffected(tag commandTag, op string) error {
	if tag.RowsAffected() == 0 {
		return fmtWrap(op, ErrCertNotFound)
	}
	return nil
}

// commandTag is the pgx Exec result (interface — avoids pgconn coupling).
type commandTag = interface {
	RowsAffected() int64
}
