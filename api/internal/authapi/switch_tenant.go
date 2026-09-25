package authapi

import (
	"context"
	"net/http"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
)

type withSwitchCookie struct {
	gen.SwitchTenantResponseObject
	created authpkg.Created
}

func (w withSwitchCookie) VisitSwitchTenantResponse(rw http.ResponseWriter) error {
	authpkg.SetCookie(rw, w.created.Token, w.created.Session.ExpiresAt.Time)
	return w.SwitchTenantResponseObject.VisitSwitchTenantResponse(rw)
}

// SwitchTenant implements gen.StrictServerInterface. The session token is
// rotated (not just its active_tenant_id updated) as a fixation defence:
// a session that briefly had access to tenant A's data gets a fresh
// identifier once it moves to tenant B.
func (h *AuthAPI) SwitchTenant(ctx context.Context, req gen.SwitchTenantRequestObject) (gen.SwitchTenantResponseObject, error) {
	sess, _ := authpkg.FromCtx(ctx)
	r := httpx.RequestFromCtx(ctx)
	requestedTenantID := req.Body.TenantId

	_, err := h.Queries.GetMembership(ctx, dbgen.GetMembershipParams{
		TenantID: idconv.ToPg(requestedTenantID),
		UserID:   idconv.ToPg(sess.UserID),
	})
	if err != nil {
		detail := "not a member of that tenant"
		return gen.SwitchTenant404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	created, err := h.Store.Rotate(ctx, qtx, sess.ID, &requestedTenantID)
	if err != nil {
		return nil, err
	}

	if err := audit.Record(ctx, qtx, audit.Entry{
		TenantID:    &requestedTenantID,
		ActorUserID: &sess.UserID,
		Action:      "tenant_switched",
		RemoteAddr:  r.RemoteAddr,
		UserAgent:   r.UserAgent(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	user, err := h.Queries.GetUserByID(ctx, idconv.ToPg(sess.UserID))
	if err != nil {
		return nil, err
	}
	memberships, err := h.Queries.ListMembershipsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	me := buildMe(sess.UserID, user.Email, &requestedTenantID, memberships)

	return withSwitchCookie{SwitchTenantResponseObject: gen.SwitchTenant200JSONResponse(me), created: created}, nil
}
