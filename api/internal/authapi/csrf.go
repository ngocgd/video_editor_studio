package authapi

import (
	"context"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/csrf"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
)

// GetCsrf implements gen.StrictServerInterface. The token is derived
// deterministically from the caller's own session token (see package
// csrf), so this is a pure computation with no database write and no
// side effect: calling it twice, from two tabs, or after a cross-site
// navigation that still sends the Lax cookie, always returns the same
// value and never invalidates anything.
func (h *AuthAPI) GetCsrf(ctx context.Context, _ gen.GetCsrfRequestObject) (gen.GetCsrfResponseObject, error) {
	r := httpx.RequestFromCtx(ctx)
	token := authpkg.TokenFromRequest(r)
	return gen.GetCsrf200JSONResponse{Token: csrf.Derive(h.CSRFPepper, token)}, nil
}
