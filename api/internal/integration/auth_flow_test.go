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
	requireStatus(t, resp, http.StatusForbidden)
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := ownerPool(t)
	q := gen.New(pool)
	fx := createFixtureUser(t, q, "auth-flow-badpw-tenant", uniqueEmail("badpw"), "owner")

	body := map[string]string{"email": fx.Email, "password": "totally-wrong-password"}
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
	requireStatus(t, resp, http.StatusForbidden)
}
