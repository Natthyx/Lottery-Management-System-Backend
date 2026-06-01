package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// NewPool creates a production-grade PostgreSQL connection pool.
//
// WHY pgxpool instead of database/sql:
//   - pgxpool is purpose-built for PostgreSQL and uses the binary protocol
//     (faster than the text protocol used by lib/pq)
//   - Built-in connection health checks and automatic reconnection
//   - First-class support for PostgreSQL-specific types (arrays, JSONB, etc.)
//
// Pool sizing rationale:
//   MaxConns = 25 — with multiple app replicas each capped at 25,
//   we leave room for migrations, monitoring agents, and admin queries
//   within Postgres's default max_connections of 100.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	// Fail fast — verify connectivity at startup, not on first request
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	log.Info().
		Int32("max_conns", config.MaxConns).
		Msg("PostgreSQL connection pool established")

	return pool, nil
}
