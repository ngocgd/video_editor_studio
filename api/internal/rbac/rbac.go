// Package rbac enforces the minimum role each route requires, read from
// the OpenAPI spec's x-min-role extension so the spec stays the single
// source of truth (openapi/paths/*.yaml) instead of a second Go map that
// could drift from it.
package rbac

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"

	"loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// Role ranks owner > editor > viewer > authenticated > public.
// "authenticated" (a valid session, no tenant requirement) is distinct
// from "viewer" (a valid session AND a resolved, at-least-viewer tenant):
// account-level routes (me, csrf, logout, switch-tenant) use
// "authenticated" because a session with no active tenant yet, or a
// membership that was revoked mid-session, must still be able to reach
// them — deny-by-default otherwise means the ONLY way such a session can
// reach any of them is if a "viewer" route silently skipped the tenant
// check, which is the trap the previous single-tier design fell into.
type Role int

const (
	RolePublic Role = iota
	RoleAuthenticated
	RoleViewer
	RoleEditor
	RoleOwner
)

// ParseRole parses the x-min-role extension value.
func ParseRole(s string) (Role, error) {
	switch s {
	case "public":
		return RolePublic, nil
	case "authenticated":
		return RoleAuthenticated, nil
	case "viewer":
		return RoleViewer, nil
	case "editor":
		return RoleEditor, nil
	case "owner":
		return RoleOwner, nil
	default:
		return 0, fmt.Errorf("rbac: unknown x-min-role %q", s)
	}
}

// BuildMinRoles walks every operation in spec and returns its declared
// minimum role, keyed by operationId. It errors if any operation is
// missing x-min-role (deny-by-default: a route that forgets to declare a
// role fails at startup, not silently at request time) or declares an
// unrecognised value.
func BuildMinRoles(spec *openapi3.T) (map[string]Role, error) {
	roles := make(map[string]Role)
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" {
				return nil, fmt.Errorf("rbac: %s %s has no operationId", method, path)
			}
			raw, ok := op.Extensions["x-min-role"]
			if !ok {
				return nil, fmt.Errorf("rbac: operation %q (%s %s) declares no x-min-role", op.OperationID, method, path)
			}
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("rbac: operation %q has a non-string x-min-role", op.OperationID)
			}
			role, err := ParseRole(s)
			if err != nil {
				return nil, fmt.Errorf("rbac: operation %q: %w", op.OperationID, err)
			}
			roles[op.OperationID] = role
		}
	}
	return roles, nil
}

// Middleware returns an oapi-codegen StrictMiddlewareFunc enforcing
// minRoles for every operation. It must run after auth.Middleware (which
// attaches auth.Session and tenant.Info to context).
func Middleware(minRoles map[string]Role) gen.StrictMiddlewareFunc {
	return func(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			minRole, ok := minRoles[operationID]
			if !ok {
				// BuildMinRoles already guarantees every spec operation is
				// present; this only guards against a future handler wired
				// up outside the generated router.
				return nil, denyProblem(w, "operation has no declared minimum role")
			}
			if minRole == RolePublic {
				return f(ctx, w, r, request)
			}

			if _, authenticated := auth.FromCtx(ctx); !authenticated {
				return nil, unauthorizedProblem(w)
			}
			if minRole == RoleAuthenticated {
				return f(ctx, w, r, request)
			}

			// RoleViewer and above always require a resolved tenant: a
			// session with no active tenant, or a membership revoked
			// mid-session, is correctly 403'd here rather than reaching a
			// handler that would otherwise panic on tenant.MustFromCtx.
			info, err := tenant.FromCtx(ctx)
			if err != nil {
				return nil, forbiddenProblem(w)
			}
			if !roleFromTenant(info.Role).satisfies(minRole) {
				return nil, forbiddenProblem(w)
			}
			return f(ctx, w, r, request)
		}
	}
}

func (r Role) satisfies(target Role) bool { return r >= target }

func roleFromTenant(role string) Role {
	r, err := ParseRole(role)
	if err != nil {
		return RolePublic
	}
	return r
}

func unauthorizedProblem(w http.ResponseWriter) error {
	httpx.WriteProblem(w, httpx.Problem{Title: "authentication required", Status: http.StatusUnauthorized})
	return nil
}

func forbiddenProblem(w http.ResponseWriter) error {
	httpx.WriteProblem(w, httpx.Problem{Title: "insufficient role for this tenant", Status: http.StatusForbidden})
	return nil
}

func denyProblem(w http.ResponseWriter, detail string) error {
	httpx.WriteProblem(w, httpx.Problem{Title: "not permitted", Status: http.StatusForbidden, Detail: detail})
	return nil
}
