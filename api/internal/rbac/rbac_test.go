package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/tenant"
)

func specWithRoles(t *testing.T, roles map[string]string) *openapi3.T {
	t.Helper()
	spec := &openapi3.T{Paths: openapi3.NewPaths()}
	for op, role := range roles {
		item := &openapi3.PathItem{
			Get: &openapi3.Operation{
				OperationID: op,
				Extensions:  map[string]any{"x-min-role": role},
				Responses:   openapi3.NewResponses(),
			},
		}
		spec.Paths.Set("/"+op, item)
	}
	return spec
}

func TestBuildMinRolesParsesDeclaredRoles(t *testing.T) {
	spec := specWithRoles(t, map[string]string{"opA": "viewer", "opB": "owner"})
	roles, err := BuildMinRoles(spec)
	if err != nil {
		t.Fatal(err)
	}
	if roles["opA"] != RoleViewer || roles["opB"] != RoleOwner {
		t.Fatalf("unexpected roles: %+v", roles)
	}
}

func TestBuildMinRolesFailsClosedOnMissingRole(t *testing.T) {
	spec := &openapi3.T{Paths: openapi3.NewPaths()}
	spec.Paths.Set("/no-role", &openapi3.PathItem{
		Get: &openapi3.Operation{OperationID: "noRole", Responses: openapi3.NewResponses()},
	})
	if _, err := BuildMinRoles(spec); err == nil {
		t.Fatal("expected an error for an operation with no x-min-role")
	}
}

func TestBuildMinRolesFailsOnUnknownRole(t *testing.T) {
	spec := specWithRoles(t, map[string]string{"opA": "superadmin"})
	if _, err := BuildMinRoles(spec); err == nil {
		t.Fatal("expected an error for an unrecognised x-min-role value")
	}
}

func TestMiddlewarePublicBypassesAuth(t *testing.T) {
	mw := Middleware(map[string]Role{"op": RolePublic})
	called := false
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		called = true
		return nil, nil
	}, "op")
	_, err := f(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if err != nil || !called {
		t.Fatalf("expected public route to reach the handler, err=%v called=%v", err, called)
	}
}

func TestMiddlewareRequiresSessionForViewerPlus(t *testing.T) {
	mw := Middleware(map[string]Role{"op": RoleViewer})
	called := false
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		called = true
		return nil, nil
	}, "op")
	w := httptest.NewRecorder()
	_, _ = f(context.Background(), w, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if called {
		t.Fatal("expected the handler not to be reached without a session")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMiddlewareForbidsInsufficientTenantRole(t *testing.T) {
	mw := Middleware(map[string]Role{"op": RoleOwner})
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		t.Fatal("handler should not be reached")
		return nil, nil
	}, "op")

	ctx := authpkg.WithSession(context.Background(), authpkg.Session{})
	ctx = tenant.WithInfo(ctx, tenant.Info{Role: "viewer"})
	w := httptest.NewRecorder()
	_, _ = f(ctx, w, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestMiddlewareAuthenticatedTierAllowsSessionWithNoTenant(t *testing.T) {
	// The "authenticated" tier (me, csrf, logout, switch-tenant) must
	// reach the handler for a session that has no active tenant yet (or
	// whose membership was revoked mid-session), unlike "viewer" and
	// above, which now correctly requires a resolved tenant.
	mw := Middleware(map[string]Role{"op": RoleAuthenticated})
	called := false
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		called = true
		return nil, nil
	}, "op")

	ctx := authpkg.WithSession(context.Background(), authpkg.Session{})
	w := httptest.NewRecorder()
	_, _ = f(ctx, w, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if !called {
		t.Fatal("expected a session with no tenant to reach an authenticated-tier handler")
	}
}

func TestMiddlewareViewerTierRequiresTenant(t *testing.T) {
	// Unlike the "authenticated" tier, "viewer" must 403 (not reach the
	// handler) for a session with no resolved tenant: this is the fix for
	// the earlier design where a viewer route silently skipped the tenant
	// check and could panic in tenant.MustFromCtx downstream instead.
	mw := Middleware(map[string]Role{"op": RoleViewer})
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		t.Fatal("handler should not be reached")
		return nil, nil
	}, "op")

	ctx := authpkg.WithSession(context.Background(), authpkg.Session{})
	w := httptest.NewRecorder()
	_, _ = f(ctx, w, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestMiddlewareAllowsSufficientTenantRole(t *testing.T) {
	mw := Middleware(map[string]Role{"op": RoleEditor})
	called := false
	f := mw(func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		called = true
		return nil, nil
	}, "op")

	ctx := authpkg.WithSession(context.Background(), authpkg.Session{})
	ctx = tenant.WithInfo(ctx, tenant.Info{Role: "owner"})
	w := httptest.NewRecorder()
	_, _ = f(ctx, w, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if !called {
		t.Fatal("expected an owner to satisfy an editor-minimum route")
	}
}
