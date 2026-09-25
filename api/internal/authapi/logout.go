package authapi

import (
	"context"
	"net/http"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
)

type withClearedCookie struct {
	gen.LogoutResponseObject
}

func (w withClearedCookie) VisitLogoutResponse(rw http.ResponseWriter) error {
	authpkg.ClearCookie(rw)
	return w.LogoutResponseObject.VisitLogoutResponse(rw)
}

// Logout implements gen.StrictServerInterface.
func (h *AuthAPI) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	sess, _ := authpkg.FromCtx(ctx)
	r := httpx.RequestFromCtx(ctx)

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	if err := h.Store.Delete(ctx, qtx, sess.ID); err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, qtx, audit.Entry{
		TenantID:    sess.ActiveTenantID,
		ActorUserID: &sess.UserID,
		Action:      "logout",
		RemoteAddr:  r.RemoteAddr,
		UserAgent:   r.UserAgent(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return withClearedCookie{gen.Logout204Response{}}, nil
}
