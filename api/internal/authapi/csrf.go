package authapi

import (
	"context"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
)

// GetCsrf implements gen.StrictServerInterface. It issues a fresh CSRF
// token bound to the caller's existing session (the session's own token
// and cookie are untouched), since only the hash of a CSRF token is ever
// stored and the plaintext cannot be recovered once forgotten by the
// client (e.g. after a page reload that did not persist it).
func (h *AuthAPI) GetCsrf(ctx context.Context, _ gen.GetCsrfRequestObject) (gen.GetCsrfResponseObject, error) {
	sess, _ := authpkg.FromCtx(ctx)
	token, err := h.Store.RefreshCSRF(ctx, h.Queries, sess.ID)
	if err != nil {
		return nil, err
	}
	return gen.GetCsrf200JSONResponse{Token: token}, nil
}
