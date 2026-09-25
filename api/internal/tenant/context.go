// Package tenant carries the request's tenant scope through context.
//
// The tenant a request operates on comes only from the session
// (sessions.active_tenant_id), set once by the session middleware after
// looking up the session row. It is never read from a path parameter,
// query parameter, header or request body: tools/tenantctx enforces this
// at compile time by failing any handler that reads a field literally
// named "tenant_id"/"TenantId"/"tenantId" from a request object instead of
// calling FromCtx.
package tenant

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Info is the resolved tenant scope for the current request.
type Info struct {
	ID   uuid.UUID
	Role string // owner, editor or viewer; the caller's role in this tenant
}

type contextKey int

const infoKey contextKey = iota

// ErrNoTenant is returned by FromCtx when no tenant has been resolved.
var ErrNoTenant = errors.New("tenant: no tenant in context")

// WithInfo returns a context carrying the resolved tenant scope.
func WithInfo(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, infoKey, info)
}

// FromCtx returns the tenant scope resolved by the session middleware.
// Every handler and sqlc query that needs a tenant id must go through this
// function; the tenantctx analyzer fails the build if a handler instead
// pulls a tenant id out of its request object.
func FromCtx(ctx context.Context) (Info, error) {
	info, ok := ctx.Value(infoKey).(Info)
	if !ok {
		return Info{}, ErrNoTenant
	}
	return info, nil
}

// MustFromCtx panics if no tenant is present. It is only safe to call from
// code that runs strictly after the RBAC middleware, which never lets an
// authenticated, tenant-scoped route reach its handler without one.
func MustFromCtx(ctx context.Context) Info {
	info, err := FromCtx(ctx)
	if err != nil {
		panic(err)
	}
	return info
}
