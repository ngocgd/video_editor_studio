// Package dbpool wraps the pgx connection pool used by the API and the
// health readiness check.
package dbpool

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps *pgxpool.Pool so it satisfies health.Pinger.
type Pool struct {
	*pgxpool.Pool
}

// New creates a pool from a Postgres connection string.
func New(ctx context.Context, dsn string) (*Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Pool{Pool: pool}, nil
}

// Ping implements health.Pinger.
func (p *Pool) Ping(ctx context.Context) error {
	return p.Pool.Ping(ctx)
}
