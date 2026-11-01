// Package db provides the PostgreSQL connection pool for the control plane.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig tunes the pgx pool with conservative defaults.
type PoolConfig struct {
	MaxConns         int32
	MinConns         int32
	MaxConnLifetime  time.Duration
	MaxConnIdleTime  time.Duration
	HealthCheckPerio time.Duration
}

// DefaultPoolConfig returns safe defaults for the first deployment.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConns:         10,
		MinConns:         1,
		MaxConnLifetime:  30 * time.Minute,
		MaxConnIdleTime:  5 * time.Minute,
		HealthCheckPerio: 30 * time.Second,
	}
}

// NewPool creates a pgx connection pool. Connections are established lazily;
// a temporarily unavailable database does not fail startup — /readyz reports
// degradation until the pool can reach PostgreSQL.
func NewPool(ctx context.Context, databaseURL string, pc PoolConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("veritabanı URL çözümlenemedi: %w", err)
	}
	cfg.MaxConns = pc.MaxConns
	cfg.MinConns = pc.MinConns
	cfg.MaxConnLifetime = pc.MaxConnLifetime
	cfg.MaxConnIdleTime = pc.MaxConnIdleTime
	cfg.HealthCheckPeriod = pc.HealthCheckPerio

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("bağlantı havuzu oluşturulamadı: %w", err)
	}
	if logger != nil {
		logger.Info("database_pool_created", slog.Int("max_conns", int(pc.MaxConns)))
	}
	return pool, nil
}
