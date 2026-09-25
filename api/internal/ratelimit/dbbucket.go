// Package ratelimit implements the two rate limiters used by the API: a
// Postgres-backed token bucket for login/password endpoints (stays correct
// across multiple API instances) and a per-instance in-memory limiter for
// general API flood defence.
package ratelimit

import (
	"context"

	"loomtale/api/internal/db/gen"
)

// DBBucket enforces a token bucket stored in Postgres.
type DBBucket struct {
	q               *gen.Queries
	capacity        float32
	refillPerSecond float32
}

// NewDBBucket builds a bucket with the given capacity and refill rate
// (tokens/second). For example capacity=5, refillPerSecond=5.0/60 gives
// "5 per minute".
func NewDBBucket(q *gen.Queries, capacity, refillPerSecond float32) *DBBucket {
	return &DBBucket{q: q, capacity: capacity, refillPerSecond: refillPerSecond}
}

// Allow refills the bucket identified by key by elapsed time, then
// consumes one token, reporting whether the request may proceed. See
// RefillRateLimitBucket's doc comment for why this is two statements
// instead of one.
func (b *DBBucket) Allow(ctx context.Context, key string) (bool, error) {
	tokens, err := b.q.RefillRateLimitBucket(ctx, gen.RefillRateLimitBucketParams{
		BucketKey:       key,
		Capacity:        b.capacity,
		RefillPerSecond: b.refillPerSecond,
	})
	if err != nil {
		return false, err
	}
	if tokens < 1 {
		return false, nil
	}
	rows, err := b.q.ConsumeRateLimitBucket(ctx, key)
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}
