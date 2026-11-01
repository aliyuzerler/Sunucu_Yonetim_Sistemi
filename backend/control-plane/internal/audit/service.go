package audit

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service wraps the query surface for dependency injection into the HTTP API.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds an audit service backed by a pool.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// List returns a cursor-paginated page with the given filter.
func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	return Query(ctx, s.pool, f)
}
