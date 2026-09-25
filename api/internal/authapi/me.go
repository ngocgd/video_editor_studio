package authapi

import (
	"context"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
)

func buildMe(userID uuid.UUID, email string, activeTenantID *uuid.UUID, memberships []dbgen.ListMembershipsForUserRow) gen.Me {
	tenants := make([]gen.TenantMembership, 0, len(memberships))
	var activeRole *gen.Role
	for _, m := range memberships {
		id := idconv.FromPg(m.TenantID)
		role := gen.Role(m.Role)
		tenants = append(tenants, gen.TenantMembership{TenantId: id, TenantName: m.TenantName, Role: role})
		if activeTenantID != nil && id == *activeTenantID {
			activeRole = &role
		}
	}
	return gen.Me{
		UserId:         userID,
		Email:          email,
		Tenants:        tenants,
		ActiveTenantId: activeTenantID,
		ActiveRole:     activeRole,
	}
}

// GetMe implements gen.StrictServerInterface.
func (h *AuthAPI) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	sess, _ := authpkg.FromCtx(ctx) // RBAC already guaranteed a session for this route
	user, err := h.Queries.GetUserByID(ctx, idconv.ToPg(sess.UserID))
	if err != nil {
		return nil, err
	}
	memberships, err := h.Queries.ListMembershipsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	me := buildMe(sess.UserID, user.Email, activeTenantFromCtx(ctx), memberships)
	return gen.GetMe200JSONResponse(me), nil
}

func activeTenantFromCtx(ctx context.Context) *uuid.UUID {
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return nil
	}
	id := info.ID
	return &id
}
