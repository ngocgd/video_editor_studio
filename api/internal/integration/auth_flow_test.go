//go:build integration

package integration

import (
	"net/http"
	"testing"

	"loomtale/api/internal/db/gen"
)

func TestOwnerLoginAndMe(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	fx := createFixtureUser(t, q, "auth-flow-owner-tenant", uniqueEmail("owner"), "owner")
	sess := login(t, fx.Email, fx.Password)

	resp := sess.do(http.MethodGet, "/auth/me", nil)
	requireStatus(t, resp, http.StatusOK)
	var me struct {
		ActiveRole string `json:"activeRole"`
		Email      string `json:"email"`
	}
	decodeJSON(t, resp, &me)
	if me.ActiveRole != "owner" {
		t.Fatalf("activeRole = %q, want owner", me.ActiveRole)
	}
	if me.Email != fx.Email {
		t.Fatalf("email = %q, want %q", me.Email, fx.Email)
	}
}

func TestViewerGetsForbiddenOnEditorRoute(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)

	fx := createFixtureUser(t, q, "auth-flow-viewer-tenant", uniqueEmail("viewer"), "viewer")
	sess := login(t, fx.Email, fx.Password)

	resp := sess.do(http.MethodPost, "/assets/presign", map[string]any{
		"kind": "image", "mime": "image/png", "bytes": 100,
	})
	requireProblem(t, resp, http.StatusForbidden, "insufficient role for this tenant")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-badpw-tenant", uniqueEmail("badpw"), "owner")

	body := map[string]string{"email": fx.Email, "password": "totally-wrong-password"}
	isolateLoginIPBudget(t)
	resp := doUnauthenticated(t, http.MethodPost, "/auth/login", body)
	requireStatus(t, resp, http.StatusUnauthorized)
}

func TestUnauthenticatedRequestIsRejected(t *testing.T) {
	skipIfAPIUnreachable(t)
	resp, err := http.Get(baseURL() + "/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", resp.StatusCode)
	}
}

func TestCSRFRejectsRequestMissingToken(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-csrf-tenant", uniqueEmail("csrf"), "owner")
	sess := login(t, fx.Email, fx.Password)
	sess.csrfToken = "" // simulate a client that forgot to attach the header

	resp := sess.do(http.MethodPost, "/auth/logout", nil)
	requireProblem(t, resp, http.StatusForbidden, "CSRF check failed")
}

func TestCSRFRejectsWrongToken(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-csrf-wrong-tenant", uniqueEmail("csrf-wrong"), "owner")
	sess := login(t, fx.Email, fx.Password)
	sess.csrfToken = "not-the-real-token"

	resp := sess.do(http.MethodPost, "/auth/logout", nil)
	requireProblem(t, resp, http.StatusForbidden, "CSRF check failed")
}

func TestCSRFRejectsBadOrigin(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-bad-origin-tenant", uniqueEmail("bad-origin"), "owner")
	sess := login(t, fx.Email, fx.Password)

	req, err := http.NewRequest(http.MethodPost, baseURL()+"/auth/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.cookie})
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("X-CSRF-Token", sess.csrfToken)
	resp, err := sess.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, resp, http.StatusForbidden, "CSRF check failed")
}

func TestOldCookieRejectedAfterLogout(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-old-cookie-logout-tenant", uniqueEmail("logout"), "owner")
	sess := login(t, fx.Email, fx.Password)

	oldCookie := sess.cookie
	requireStatus(t, sess.do(http.MethodPost, "/auth/logout", nil), http.StatusNoContent)

	sess.cookie = oldCookie
	resp := sess.do(http.MethodGet, "/auth/me", nil)
	requireStatus(t, resp, http.StatusUnauthorized)
}

func TestOldCookieRejectedAfterSwitchTenant(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-old-cookie-switch-tenant", uniqueEmail("switch-rotate"), "owner")
	sess := login(t, fx.Email, fx.Password)

	oldCookie := sess.cookie
	resp := sess.do(http.MethodPost, "/auth/switch-tenant", map[string]string{"tenantId": fx.TenantID.String()})
	requireStatus(t, resp, http.StatusOK)

	sess.cookie = oldCookie
	meResp := sess.do(http.MethodGet, "/auth/me", nil)
	requireStatus(t, meResp, http.StatusUnauthorized)
}
