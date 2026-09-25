package assetsapi

import (
	"context"
	"net/http"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/tenant"
)

// GetAsset implements gen.StrictServerInterface. A cross-tenant request
// (an id that belongs to a different tenant) gets the same 404 as a
// genuinely missing id: the query itself is tenant-scoped, so there is no
// separate branch that could leak existence.
func (h *AssetsAPI) GetAsset(ctx context.Context, req gen.GetAssetRequestObject) (gen.GetAssetResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{
		TenantID: idconv.ToPg(info.ID),
		ID:       idconv.ToPg(req.Id),
	})
	if err != nil {
		detail := "asset not found"
		return gen.GetAsset404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}

	var downloadURL *string
	if asset.Status == "ready" {
		url, err := h.Browser.PresignGet(ctx, asset.StorageKey)
		if err != nil {
			return nil, err
		}
		downloadURL = &url
	}

	return gen.GetAsset200JSONResponse(toAssetDTO(asset, downloadURL)), nil
}
