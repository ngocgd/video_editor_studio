// Package a is fixture data for the tenantctx analyzer test: a
// self-contained stand-in for the real oapi-codegen request-object shape
// (Params/Body) and a sqlc-style Params struct.
package a

type reqParams struct{ TenantId string }
type reqBody struct{ TenantId string }
type request struct {
	Params reqParams
	Body   *reqBody
}

type queryParams struct {
	TenantID string
}

func directFromParams(req request) queryParams {
	return queryParams{TenantID: req.Params.TenantId} // want `tenantctx: TenantID is populated from a request object`
}

func directFromBody(req request) queryParams {
	return queryParams{TenantID: req.Body.TenantId} // want `tenantctx: TenantID is populated from a request object`
}

func allowedException(req request) queryParams {
	// tenantctx:allow: switch-tenant reads its target tenant from the body by design, verified via membership check
	return queryParams{TenantID: req.Body.TenantId}
}

func fromResolvedTenant(resolvedTenantID string) queryParams {
	return queryParams{TenantID: resolvedTenantID}
}

func viaLocalVariable(req request) queryParams {
	id := req.Body.TenantId
	return queryParams{TenantID: id} // want `tenantctx: TenantID is populated from a request object`
}

func viaLocalVariableFromParams(req request) queryParams {
	var id string
	id = req.Params.TenantId
	return queryParams{TenantID: id} // want `tenantctx: TenantID is populated from a request object`
}

func viaLocalVariableWithAllowComment(req request) queryParams {
	id := req.Body.TenantId
	// tenantctx:allow: same verified switch-tenant shape as allowedException, laundered through a local variable
	return queryParams{TenantID: id}
}

type multiField struct {
	TenantID string
	UserID   string
}

func allowCommentCoversWholeLiteral(req request) multiField {
	// tenantctx:allow: comment sits above the literal, not the specific field, and must still cover it
	return multiField{
		TenantID: req.Body.TenantId,
		UserID:   "u1",
	}
}
