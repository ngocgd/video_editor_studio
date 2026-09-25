package auth

import (
	"log/slog"
	"net/http"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// Middleware resolves the session cookie (if any) into context. It never
// rejects a request itself on a missing/invalid cookie: routes with no
// valid session simply see no auth.Session in context, and the RBAC
// middleware downstream is what turns that into a 401 for routes that
// require one. This keeps public routes (health, login) working through
// the same middleware chain. A genuine backend error (the database is
// down), however, is answered with 500 here rather than silently falling
// through as "unauthenticated" — an outage must not look like every
// session logged out at once.
func Middleware(store Store, q *gen.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := TokenFromRequest(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			row, ok, err := store.Lookup(r.Context(), q, token)
			if err != nil {
				slog.ErrorContext(r.Context(), "session lookup failed", "error", err)
				httpx.WriteProblem(w, httpx.Problem{Title: "internal error", Status: http.StatusInternalServerError})
				return
			}
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			if err := store.Touch(r.Context(), q, idconv.FromPg(row.ID), idconv.FromPgTimestamptz(row.LastSeenAt)); err != nil {
				slog.ErrorContext(r.Context(), "session touch failed", "error", err)
			}

			sess := Session{
				ID:             idconv.FromPg(row.ID),
				UserID:         idconv.FromPg(row.UserID),
				ActiveTenantID: idconv.FromPgPtr(row.ActiveTenantID),
				ExpiresAt:      idconv.FromPgTimestamptz(row.ExpiresAt),
			}
			ctx := WithSession(r.Context(), sess)

			if sess.ActiveTenantID != nil {
				membership, err := q.GetMembership(ctx, gen.GetMembershipParams{
					TenantID: idconv.ToPg(*sess.ActiveTenantID),
					UserID:   idconv.ToPg(sess.UserID),
				})
				if err == nil {
					ctx = tenant.WithInfo(ctx, tenant.Info{ID: *sess.ActiveTenantID, Role: membership.Role})
				}
				// A lookup error here (e.g. membership revoked mid-session)
				// simply leaves no tenant.Info in context; RBAC then denies
				// any tenant-scoped route for this request.
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CSRFLookup adapts auth's session context into the narrow interface the
// csrf package needs, without csrf importing auth. It returns the
// plaintext session token (read straight from the request cookie, never
// stored anywhere) that the CSRF token is derived from.
func CSRFLookup(r *http.Request) (string, bool) {
	if _, ok := FromCtx(r.Context()); !ok {
		return "", false
	}
	return TokenFromRequest(r), true
}
