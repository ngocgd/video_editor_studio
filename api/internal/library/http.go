package library

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/diskguard"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// LibraryAPI implements the library slice of gen.StrictServerInterface.
type LibraryAPI struct {
	Service *Service
	// Disk is the render/model-pull watermark; nil shows it as unknown.
	Disk *diskguard.Watermark
}

func problem(status int, title string, err error) gen.Problem {
	p := gen.Problem{Title: title, Status: status}
	if err != nil {
		detail := err.Error()
		p.Detail = &detail
	}
	return p
}

// ListLibraryAssets implements gen.StrictServerInterface.
func (h *LibraryAPI) ListLibraryAssets(ctx context.Context, req gen.ListLibraryAssetsRequestObject) (gen.ListLibraryAssetsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	f := ListFilter{SeriesID: req.Params.SeriesId, Before: req.Params.Cursor}
	if req.Params.Kind != nil {
		f.Kind = string(*req.Params.Kind)
	}
	if req.Params.Limit != nil {
		f.Limit = *req.Params.Limit
	}
	page, err := h.Service.List(ctx, info.ID, f)
	if err != nil {
		return nil, err
	}
	resp := gen.ListLibraryAssets200JSONResponse{Items: make([]gen.LibraryAsset, 0, len(page.Items)), NextCursor: page.Next}
	for _, r := range page.Items {
		item := gen.LibraryAsset{
			Id: idconv.FromPg(r.ID), Kind: gen.AssetKind(r.Kind), Mime: r.Mime, Bytes: r.Bytes.Int64, Status: r.Status,
			CreatedAt: idconv.FromPgTimestamptz(r.CreatedAt), SeriesId: idconv.FromPgPtr(r.SeriesID),
			ReferencedBy: r.ReferencedBy,
		}
		if item.ReferencedBy == nil {
			item.ReferencedBy = []string{}
		}
		if r.SeriesTitle.Valid {
			title := r.SeriesTitle.String
			item.SeriesTitle = &title
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

// GetLibraryUsage implements gen.StrictServerInterface.
func (h *LibraryAPI) GetLibraryUsage(ctx context.Context, _ gen.GetLibraryUsageRequestObject) (gen.GetLibraryUsageResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	u, err := h.Service.Usage(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	resp := gen.GetLibraryUsage200JSONResponse{
		Series: make([]gen.LibrarySeriesUsage, 0, len(u.Series)), TotalBytes: u.TotalBytes, Assets: u.Assets,
		Disk: diskguard.APIStatus(ctx, h.Disk),
	}
	for _, s := range u.Series {
		item := gen.LibrarySeriesUsage{SeriesId: idconv.FromPgPtr(s.SeriesID), Assets: s.Assets, Bytes: s.Bytes}
		if s.SeriesTitle.Valid {
			title := s.SeriesTitle.String
			item.SeriesTitle = &title
		}
		resp.Series = append(resp.Series, item)
	}
	return resp, nil
}

// GetLibrarySettings implements gen.StrictServerInterface.
func (h *LibraryAPI) GetLibrarySettings(ctx context.Context, _ gen.GetLibrarySettingsRequestObject) (gen.GetLibrarySettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	s, err := h.Service.Settings(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	return gen.GetLibrarySettings200JSONResponse(settingsToAPI(s)), nil
}

// PutLibrarySettings implements gen.StrictServerInterface.
func (h *LibraryAPI) PutLibrarySettings(ctx context.Context, req gen.PutLibrarySettingsRequestObject) (gen.PutLibrarySettingsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	s, err := h.Service.UpdateSettings(ctx, info.ID, req.Body.SegmentTtlDays, req.Body.TakeTtlDays)
	if errors.Is(err, pipeline.ErrValidation) {
		return gen.PutLibrarySettings400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "invalid retention", err)), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutLibrarySettings200JSONResponse(settingsToAPI(s)), nil
}

// PreviewLibraryCleanup implements gen.StrictServerInterface.
func (h *LibraryAPI) PreviewLibraryCleanup(ctx context.Context, _ gen.PreviewLibraryCleanupRequestObject) (gen.PreviewLibraryCleanupResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	p, err := h.Service.Preview(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	return gen.PreviewLibraryCleanup200JSONResponse{
		Settings: settingsToAPI(p.Settings), Segments: candidatesToAPI(p.Segments), Takes: candidatesToAPI(p.Takes),
		Bytes: p.Bytes, Truncated: p.Truncated, Token: p.Token,
	}, nil
}

// ConfirmLibraryCleanup implements gen.StrictServerInterface.
func (h *LibraryAPI) ConfirmLibraryCleanup(ctx context.Context, req gen.ConfirmLibraryCleanupRequestObject) (gen.ConfirmLibraryCleanupResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	var actor Actor
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		id := sess.UserID
		actor.UserID = &id
	}
	if r := httpx.RequestFromCtx(ctx); r != nil {
		actor.RemoteAddr, actor.UserAgent = r.RemoteAddr, r.UserAgent()
	}
	runID, p, err := h.Service.Confirm(ctx, info.ID, req.Body.Token, actor)
	switch {
	case err == nil:
	case errors.Is(err, ErrPreviewChanged):
		return gen.ConfirmLibraryCleanup409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "cleanup candidates changed; preview again", nil)), nil
	case errors.Is(err, pipeline.ErrQuotaExceeded):
		return gen.ConfirmLibraryCleanup429ApplicationProblemPlusJSONResponse(problem(http.StatusTooManyRequests, "quota exceeded", err)), nil
	case errors.Is(err, pipeline.ErrValidation):
		return gen.ConfirmLibraryCleanup422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "nothing to clean up", nil)), nil
	default:
		return nil, err
	}
	return gen.ConfirmLibraryCleanup202JSONResponse{RunId: runID, Segments: len(p.Segments), Takes: len(p.Takes), Bytes: p.Bytes}, nil
}

func settingsToAPI(s Settings) gen.LibrarySettings {
	return gen.LibrarySettings{SegmentTtlDays: s.SegmentTTLDays, TakeTtlDays: s.TakeTTLDays, LastCleanupAt: s.LastCleanupAt}
}

func candidatesToAPI(cs []Candidate) []gen.CleanupCandidate {
	out := make([]gen.CleanupCandidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, gen.CleanupCandidate{Id: c.ID, Kind: c.Kind, AssetId: c.AssetID, Bytes: c.Bytes, Since: c.Since})
	}
	return out
}
