package assetsapi

import (
	"context"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/tenant"
)

// ListAssets implements gen.StrictServerInterface: cursor pagination over
// (tenant_id, id) with UUIDv7 ordering.
func (h *AssetsAPI) ListAssets(ctx context.Context, req gen.ListAssetsRequestObject) (gen.ListAssetsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	cursorStr := ""
	if req.Params.Cursor != nil {
		cursorStr = *req.Params.Cursor
	}
	cursor, err := httpx.DecodeCursor(cursorStr)
	if err != nil {
		cursor = uuid.Nil
	}
	limit := httpx.PageLimit(req.Params.Limit)

	rows, err := h.Queries.ListAssets(ctx, dbgen.ListAssetsParams{
		TenantID:  idconv.ToPg(info.ID),
		Cursor:    idconv.ToPg(cursor),
		PageLimit: limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]gen.Asset, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAssetDTO(row, nil))
	}

	list := gen.AssetList{Items: items}
	if int32(len(rows)) == limit && len(rows) > 0 {
		next := httpx.EncodeCursor(idconv.FromPg(rows[len(rows)-1].ID))
		list.NextCursor = &next
	}
	return gen.ListAssets200JSONResponse(list), nil
}
