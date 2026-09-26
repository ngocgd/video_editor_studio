package media

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/tenant"
)

// backfillPage bounds how many assets one backfill call queues; calling
// it again picks up the rest.
const backfillPage = 500

// MediaAPI implements the media slice of gen.StrictServerInterface.
type MediaAPI struct {
	Queries *dbgen.Queries
	Engine  *pipeline.Engine
	Browser *storage.Browser
}

// GetAssetVariant redirects to a short-lived browser URL of an asset of
// the caller's tenant, or of one of its image variants. Pages reference
// this route instead of embedding hundreds of presigned URLs in a list
// response.
func (h *MediaAPI) GetAssetVariant(ctx context.Context, req gen.GetAssetVariantRequestObject) (gen.GetAssetVariantResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil || asset.Status != "ready" {
		return gen.GetAssetVariant404ApplicationProblemPlusJSONResponse{Title: "asset not found", Status: http.StatusNotFound}, nil
	}
	key, version := asset.StorageKey, asset.StorageVersionID.String
	if req.Variant != gen.Original {
		key, version = DecodeVariants(asset.Variants).VariantKey(string(req.Variant)), ""
		if key == "" {
			return gen.GetAssetVariant404ApplicationProblemPlusJSONResponse{Title: "variant not found", Status: http.StatusNotFound}, nil
		}
	}
	url, err := h.Browser.PresignGet(ctx, key, version)
	if err != nil {
		return nil, err
	}
	return gen.GetAssetVariant302Response{Headers: gen.GetAssetVariant302ResponseHeaders{Location: url}}, nil
}

// BackfillMedia queues variants and peaks for ready assets that predate
// the media steps.
func (h *MediaAPI) BackfillMedia(ctx context.Context, _ gen.BackfillMediaRequestObject) (gen.BackfillMediaResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	assets, err := h.Queries.ListAssetsMissingDerivatives(ctx, dbgen.ListAssetsMissingDerivativesParams{TenantID: idconv.ToPg(info.ID), PageLimit: backfillPage})
	if err != nil {
		return nil, err
	}
	var createdBy *uuid.UUID
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		createdBy = &sess.UserID
	}
	runID, _, err := EnqueueDerivatives(ctx, h.Engine, info.ID, assets, createdBy)
	if err != nil {
		return nil, err
	}
	resp := gen.BackfillMedia202JSONResponse{}
	for _, a := range assets {
		if a.Kind == "image" {
			resp.Variants++
		} else {
			resp.Peaks++
		}
	}
	if runID != uuid.Nil {
		resp.RunId = &runID
	}
	return resp, nil
}
