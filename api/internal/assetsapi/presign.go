package assetsapi

import (
	"context"
	"net/http"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/tenant"
)

// PresignAsset implements gen.StrictServerInterface. RBAC guarantees an
// editor+ role in a resolved tenant for this route.
func (h *AssetsAPI) PresignAsset(ctx context.Context, req gen.PresignAssetRequestObject) (gen.PresignAssetResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	kind := string(req.Body.Kind)

	if err := storage.ValidatePresign(kind, req.Body.Mime, req.Body.Bytes); err != nil {
		detail := err.Error()
		return gen.PresignAsset400ApplicationProblemPlusJSONResponse{Title: "invalid asset", Status: http.StatusBadRequest, Detail: &detail}, nil
	}

	assetID := idconv.NewV7()
	key := storage.Key(info.ID.String(), kind, assetID)

	_, err := h.Queries.CreateAsset(ctx, dbgen.CreateAssetParams{
		ID:         idconv.ToPg(assetID),
		TenantID:   idconv.ToPg(info.ID),
		Kind:       kind,
		StorageKey: key,
		Mime:       req.Body.Mime,
		CreatedBy:  idconv.ToPg(sess.UserID),
	})
	if err != nil {
		return nil, err
	}

	policy, err := h.Browser.PresignPost(ctx, info.ID.String(), key, req.Body.Mime, 1, req.Body.Bytes)
	if err != nil {
		return nil, err
	}

	return gen.PresignAsset201JSONResponse{
		AssetId:   assetID,
		UploadUrl: policy.UploadURL,
		Fields:    policy.Fields,
		ExpiresAt: policy.ExpiresAt,
	}, nil
}
