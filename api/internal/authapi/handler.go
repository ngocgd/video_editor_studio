// Package authapi implements the auth slice of the generated strict
// server interface (gen.StrictServerInterface): login, logout, me,
// switch-tenant and CSRF token issuance. It is combined with the other
// domain handler packages into one server in cmd/api, where the full
// interface is verified at compile time.
package authapi

import (
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/dbpool"
	"loomtale/api/internal/ratelimit"
)

// AuthAPI implements the auth slice of gen.StrictServerInterface. It is
// named distinctly (not "Handler") because the composite server in
// cmd/api embeds every domain handler by type, and Go's embedded-field
// promotion needs each embedded type name to be unique.
type AuthAPI struct {
	Pool         *dbpool.Pool
	Queries      *dbgen.Queries
	Store        authpkg.Store
	LoginPerUser *ratelimit.DBBucket // key: ip+email, 5/min
	LoginPerIP   *ratelimit.DBBucket // key: ip, 20/hour
}
