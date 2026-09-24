// Package health implements the liveness and readiness handlers.
package health

import "context"

// Pinger checks that a dependency is reachable. Implementations wrap the
// Postgres pool and the MinIO client; the readiness handler fans out to all
// of them.
type Pinger interface {
	Ping(ctx context.Context) error
}
