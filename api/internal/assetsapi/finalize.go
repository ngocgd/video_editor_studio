package assetsapi

import (
	"context"
	"errors"
	"net/http"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/tenant"
)

// FinalizeAsset implements gen.StrictServerInterface: it verifies an
// uploaded object (HEAD + a 512B sniff, never the full body) before
// marking the asset ready.
func (h *AssetsAPI) FinalizeAsset(ctx context.Context, req gen.FinalizeAssetRequestObject) (gen.FinalizeAssetResponseObject, error) {
	info := tenant.MustFromCtx(ctx)

	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{
		TenantID: idconv.ToPg(info.ID),
		ID:       idconv.ToPg(req.Id),
	})
	if err != nil {
		detail := "asset not found"
		return gen.FinalizeAsset404ApplicationProblemPlusJSONResponse{Title: "not found", Status: http.StatusNotFound, Detail: &detail}, nil
	}

	result, err := h.Internal.Finalize(ctx, asset.StorageKey)
	if errors.Is(err, storage.ErrObjectNotFound) {
		detail := "the object was never uploaded to the presigned URL"
		return gen.FinalizeAsset422ApplicationProblemPlusJSONResponse{Title: "not uploaded", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}

	// text/* kinds have no reliable magic bytes to sniff; finalize trusts
	// the declared+allowlisted MIME captured at presign time for them
	// instead of the (always-empty) sniff result.
	effectiveMIME := result.SniffedMIME
	if asset.Kind == "document" {
		effectiveMIME = asset.Mime
	} else if !storage.KindAllowsMIME(asset.Kind, result.SniffedMIME) {
		_ = h.Queries.MarkAssetFailed(ctx, dbgen.MarkAssetFailedParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
		detail := "the uploaded object's content does not match its declared kind"
		return gen.FinalizeAsset422ApplicationProblemPlusJSONResponse{Title: "mime sniff mismatch", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}

	if maxBytes, ok := storage.MaxBytesByKind[asset.Kind]; ok && result.Size > maxBytes {
		_ = h.Queries.MarkAssetFailed(ctx, dbgen.MarkAssetFailedParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
		detail := "uploaded object exceeds the size cap for this asset kind"
		return gen.FinalizeAsset422ApplicationProblemPlusJSONResponse{Title: "too large", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	updated, err := qtx.MarkAssetReady(ctx, dbgen.MarkAssetReadyParams{
		TenantID: idconv.ToPg(info.ID),
		ID:       idconv.ToPg(req.Id),
		Bytes:    idconv.ToPgInt8(result.Size),
		Sha256:   idconv.ToPgText(result.SHA256Hex),
		Mime:     effectiveMIME,
		// Width/Height/DurationMs stay NULL here: probing media dimensions
		// needs ffprobe, which runs in the worker pipeline (a later phase),
		// not the API request path.
	})
	if err != nil {
		return nil, err
	}

	sess, _ := authpkg.FromCtx(ctx)
	r := httpx.RequestFromCtx(ctx)
	if err := audit.Record(ctx, qtx, audit.Entry{
		TenantID:    &info.ID,
		ActorUserID: &sess.UserID,
		Action:      "asset_finalized",
		TargetType:  "asset",
		TargetID:    req.Id.String(),
		RemoteAddr:  r.RemoteAddr,
		UserAgent:   r.UserAgent(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return gen.FinalizeAsset200JSONResponse(toAssetDTO(updated, nil)), nil
}
