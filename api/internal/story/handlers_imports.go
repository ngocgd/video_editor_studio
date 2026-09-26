package story

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/importer"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// importMaxBytes is the import path's own cap (per the phase contract's
// "≤10MB"), separate from and stricter-or-equal-to the generic "document"
// asset kind cap (5MiB, storage.MaxBytesByKind) it also inherits from at
// presign/finalize time.
const importMaxBytes = 10 << 20

// importMaxChapters bounds one import: a commit creates an episode and a
// draft per chapter in a single transaction.
const importMaxChapters = 500

// allowedImportMimes restricts a registered asset to plain text/markdown,
// matching the "Upload .txt or .md" requirement.
var allowedImportMimes = map[string]bool{"text/plain": true, "text/markdown": true}

// ListImports implements gen.StrictServerInterface.
func (h *StoryAPI) ListImports(ctx context.Context, req gen.ListImportsRequestObject) (gen.ListImportsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	limit := int32(defaultPageLimit)
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = int32(*req.Params.Limit)
	}
	var cursor pgtype.UUID
	if req.Params.Cursor != nil {
		if id, err := uuid.Parse(*req.Params.Cursor); err == nil {
			cursor = idconv.ToPg(id)
		}
	}
	rows, err := h.Queries.ListImports(ctx, dbgen.ListImportsParams{TenantID: idconv.ToPg(info.ID), Cursor: cursor, PageLimit: limit})
	if err != nil {
		return nil, err
	}
	items := make([]gen.Import, 0, len(rows))
	for _, r := range rows {
		dto, err := importToDTO(r)
		if err != nil {
			return nil, err
		}
		items = append(items, dto)
	}
	var nextCursor *string
	if len(rows) == int(limit) && len(rows) > 0 {
		c := idconv.FromPg(rows[len(rows)-1].ID).String()
		nextCursor = &c
	}
	return gen.ListImports200JSONResponse{Items: items, NextCursor: nextCursor}, nil
}

// CreateImport implements gen.StrictServerInterface: registers an
// already-finalized document asset for import. It never accepts raw
// bytes itself (the client already uploaded through the existing
// presigned POST + finalize flow, kind=document); this only validates the
// asset is ready, text/markdown, and within the import path's own size
// cap.
func (h *StoryAPI) CreateImport(ctx context.Context, req gen.CreateImportRequestObject) (gen.CreateImportResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Body.AssetId)})
	if err != nil {
		if isNoRows(err) {
			detail := "asset not found"
			return gen.CreateImport400ApplicationProblemPlusJSONResponse{Title: "invalid asset", Status: http.StatusBadRequest, Detail: &detail}, nil
		}
		return nil, err
	}
	if problem := validateImportAsset(asset); problem != "" {
		return gen.CreateImport400ApplicationProblemPlusJSONResponse{Title: "invalid asset", Status: http.StatusBadRequest, Detail: &problem}, nil
	}

	var seriesID pgtype.UUID // NULL (no series chosen yet) unless set below
	if req.Body.SeriesId != nil {
		if _, err := h.requireSeries(ctx, info.ID, *req.Body.SeriesId); err != nil {
			if isNoRows(err) {
				detail := "series not found"
				return gen.CreateImport400ApplicationProblemPlusJSONResponse{Title: "invalid series", Status: http.StatusBadRequest, Detail: &detail}, nil
			}
			return nil, err
		}
		seriesID = idconv.ToPg(*req.Body.SeriesId)
	}

	imp, err := h.Queries.CreateImport(ctx, dbgen.CreateImportParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), SeriesID: seriesID,
		AssetID: idconv.ToPg(req.Body.AssetId), CreatedBy: idconv.ToPgPtr(userIDPtr(sess)),
	})
	if err != nil {
		return nil, err
	}
	dto, err := importToDTO(imp)
	if err != nil {
		return nil, err
	}
	return gen.CreateImport201JSONResponse(dto), nil
}

func validateImportAsset(a dbgen.Asset) string {
	if a.Status != "ready" {
		return "asset is not finalized (status must be ready)"
	}
	if !allowedImportMimes[a.Mime] {
		return "asset must be text/plain or text/markdown"
	}
	if !a.Bytes.Valid || a.Bytes.Int64 <= 0 || a.Bytes.Int64 > importMaxBytes {
		return "asset exceeds the 10MB import size limit"
	}
	return ""
}

// GetImport implements gen.StrictServerInterface.
func (h *StoryAPI) GetImport(ctx context.Context, req gen.GetImportRequestObject) (gen.GetImportResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	imp, err := h.Queries.GetImportByID(ctx, dbgen.GetImportByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		if isNoRows(err) {
			return gen.GetImport404ApplicationProblemPlusJSONResponse{Title: "import not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	dto, err := importToDTO(imp)
	if err != nil {
		return nil, err
	}
	return gen.GetImport200JSONResponse(dto), nil
}

// PreviewImport implements gen.StrictServerInterface: fetches the asset's
// bytes from storage, detects encoding, splits into chapters, and stores
// the preview. A decode failure is reported as 422 with a specific
// detail, never silently guessed.
func (h *StoryAPI) PreviewImport(ctx context.Context, req gen.PreviewImportRequestObject) (gen.PreviewImportResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	imp, err := h.Queries.GetImportByID(ctx, dbgen.GetImportByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		if isNoRows(err) {
			detail := "import not found"
			return gen.PreviewImport422ApplicationProblemPlusJSONResponse{Title: "import not found", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
		}
		return nil, err
	}
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(info.ID), ID: imp.AssetID})
	if err != nil {
		return nil, err
	}

	obj, err := h.Internal.GetObject(ctx, h.Internal.Bucket, asset.StorageKey, importObjectOptions(asset))
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	raw, err := io.ReadAll(io.LimitReader(obj, importMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > importMaxBytes {
		detail := "the manuscript exceeds the 10MB import size limit"
		return gen.PreviewImport422ApplicationProblemPlusJSONResponse{Title: "too large", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}

	text, encodingName, err := importer.DecodeText(raw)
	if err != nil {
		detail := fmt.Sprintf("could not detect the manuscript's text encoding (expected UTF-8, UTF-16 or GB18030/GBK): %v", err)
		return gen.PreviewImport422ApplicationProblemPlusJSONResponse{Title: "decode failed", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}

	preset := importer.PresetAuto
	if req.Body != nil && req.Body.SplitPreset != nil {
		preset = presetFromDTO(*req.Body.SplitPreset)
	}
	chapters, usedPreset := importer.Split(text, preset)
	if len(chapters) > importMaxChapters {
		detail := fmt.Sprintf("the split found %d chapters; one import can hold at most %d, so split the manuscript into several files or pick another preset", len(chapters), importMaxChapters)
		return gen.PreviewImport422ApplicationProblemPlusJSONResponse{Title: "too many chapters", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
	}

	chapterDocs := make([]chapterDoc, 0, len(chapters))
	for _, c := range chapters {
		chapterDocs = append(chapterDocs, chapterDoc{Index: c.Index, Title: c.Title, CharStart: c.CharStart, CharEnd: c.CharEnd, WordCount: c.WordCount})
	}
	chaptersJSON, err := json.Marshal(chapterDocs)
	if err != nil {
		return nil, err
	}

	updated, err := h.Queries.UpdateImportPreview(ctx, dbgen.UpdateImportPreviewParams{
		Encoding: encodingName, SplitPreset: string(usedPreset), Chapters: chaptersJSON,
		TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id),
	})
	if err != nil {
		if isNoRows(err) {
			detail := "this import was already committed"
			return gen.PreviewImport422ApplicationProblemPlusJSONResponse{Title: "already committed", Status: http.StatusUnprocessableEntity, Detail: &detail}, nil
		}
		return nil, err
	}
	dto, err := importToDTO(updated)
	if err != nil {
		return nil, err
	}
	return gen.PreviewImport200JSONResponse(dto), nil
}

// CommitImport implements gen.StrictServerInterface: creates one episode
// per selected (or every previewed) chapter, tainted (origin=import), and
// optionally enqueues llm.translate steps per chapter's "en" draft when
// translateToLang is set.
func (h *StoryAPI) CommitImport(ctx context.Context, req gen.CommitImportRequestObject) (gen.CommitImportResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	imp, err := h.Queries.GetImportByID(ctx, dbgen.GetImportByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id)})
	if err != nil {
		if isNoRows(err) {
			return gen.CommitImport409ApplicationProblemPlusJSONResponse{Title: "import not found", Status: http.StatusConflict}, nil
		}
		return nil, err
	}
	if imp.Status != "preview" {
		detail := fmt.Sprintf("import must be previewed first (current status: %s)", imp.Status)
		return gen.CommitImport409ApplicationProblemPlusJSONResponse{Title: "not ready to commit", Status: http.StatusConflict, Detail: &detail}, nil
	}
	if _, err := h.requireSeries(ctx, info.ID, req.Body.SeriesId); err != nil {
		if isNoRows(err) {
			detail := "series not found"
			return gen.CommitImport409ApplicationProblemPlusJSONResponse{Title: "invalid series", Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}

	var chapters []chapterDoc
	if err := json.Unmarshal(imp.Chapters, &chapters); err != nil {
		return nil, err
	}

	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(info.ID), ID: imp.AssetID})
	if err != nil {
		return nil, err
	}
	obj, err := h.Internal.GetObject(ctx, h.Internal.Bucket, asset.StorageKey, importObjectOptions(asset))
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	raw, err := io.ReadAll(io.LimitReader(obj, importMaxBytes+1))
	if err != nil {
		return nil, err
	}
	text, _, err := importer.DecodeText(raw)
	if err != nil {
		return nil, err
	}
	sourceRunes := []rune(text)

	selected := selectChapters(chapters, req.Body.ChapterIndexes)

	// Claiming the import, creating every episode and draft, and marking it
	// committed happen in one transaction: a failure leaves nothing behind,
	// and a second commit of the same import waits on the row and is refused.
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	if _, err := qtx.MarkImportCommitted(ctx, dbgen.MarkImportCommittedParams{
		SeriesID: idconv.ToPg(req.Body.SeriesId), TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.Id),
	}); err != nil {
		if isNoRows(err) {
			detail := "this import was already committed"
			return gen.CommitImport409ApplicationProblemPlusJSONResponse{Title: "not ready to commit", Status: http.StatusConflict, Detail: &detail}, nil
		}
		return nil, err
	}

	idx, err := qtx.NextEpisodeIdx(ctx, dbgen.NextEpisodeIdxParams{TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Body.SeriesId)})
	if err != nil {
		return nil, err
	}

	episodeIDs := make([]uuid.UUID, 0, len(selected))
	var steps []pipeline.StepSpec
	for _, c := range selected {
		chapterText := ""
		if c.CharStart >= 0 && c.CharEnd <= len(sourceRunes) && c.CharStart <= c.CharEnd {
			chapterText = string(sourceRunes[c.CharStart:c.CharEnd])
		}
		title := c.Title
		if title == "" {
			title = fmt.Sprintf("Chapter %d", c.Index)
		}

		episodeID := idconv.NewV7()
		outline, _ := json.Marshal([]outlineBeatDoc{})
		episode, err := qtx.CreateEpisode(ctx, dbgen.CreateEpisodeParams{
			ID: idconv.ToPg(episodeID), TenantID: idconv.ToPg(info.ID), SeriesID: idconv.ToPg(req.Body.SeriesId),
			Idx: idx, Title: title, Outline: outline, Status: "draft",
			SourceImportChapterIndex: idconv.ToPgInt4(int32(c.Index)),
		})
		if err != nil {
			return nil, err
		}
		idx++

		paragraphs := paragraphsFromChapterText(chapterText)
		paragraphsJSON, err := encodeParagraphs(paragraphs)
		if err != nil {
			return nil, err
		}
		if _, err := qtx.CreateDraft(ctx, dbgen.CreateDraftParams{
			ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(info.ID), EpisodeID: episode.ID,
			Lang: "en", Paragraphs: paragraphsJSON, WordCount: int32(WordCount(paragraphs)),
		}); err != nil {
			return nil, err
		}

		episodeIDs = append(episodeIDs, episodeID)
		if req.Body.TranslateToLang != nil {
			steps = append(steps, pipeline.StepSpec{
				ID: idconv.NewV7(), Kind: KindTranslate, ScopeKind: ScopeEpisode, ScopeID: episodeID,
				Priority: pipeline.PriorityBatch,
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	resp := gen.ImportCommitResponse{EpisodeIds: episodeIDs}
	if len(steps) > 0 {
		runID := idconv.NewV7()
		if _, err := h.Engine.Enqueue(ctx, info.ID, pipeline.RunSpec{
			ID: runID, ScopeKind: "series", ScopeID: req.Body.SeriesId, Kind: "import.translate",
			CreatedBy: userIDPtr(sess), Steps: steps,
		}); err != nil {
			return nil, err
		}
		resp.RunId = &runID
	}
	return gen.CommitImport200JSONResponse(resp), nil
}

// chapterDoc is the per-chapter shape stored in imports.chapters jsonb.
type chapterDoc struct {
	Index     int    `json:"index"`
	Title     string `json:"title"`
	CharStart int    `json:"charStart"`
	CharEnd   int    `json:"charEnd"`
	WordCount int    `json:"wordCount"`
}

func selectChapters(all []chapterDoc, indexes *[]int) []chapterDoc {
	if indexes == nil || len(*indexes) == 0 {
		return all
	}
	want := map[int]bool{}
	for _, i := range *indexes {
		want[i] = true
	}
	var out []chapterDoc
	for _, c := range all {
		if want[c.Index] {
			out = append(out, c)
		}
	}
	return out
}

// paragraphsFromChapterText splits chapter text into paragraph-per-blank-
// line, tainted (origin=import), per the migration comment on episodes.
func paragraphsFromChapterText(text string) []Paragraph {
	blocks := splitBlankLines(text)
	paragraphs := make([]Paragraph, 0, len(blocks))
	for _, b := range blocks {
		paragraphs = append(paragraphs, Paragraph{ID: idconv.NewV7().String(), Text: b, Origin: "import", Tainted: true})
	}
	return paragraphs
}

// splitBlankLines splits text into non-empty paragraphs separated by one
// or more blank lines.
func splitBlankLines(text string) []string {
	var out []string
	for _, block := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block != "" {
			out = append(out, block)
		}
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = []string{strings.TrimSpace(text)}
	}
	return out
}

func importToDTO(imp dbgen.Import) (gen.Import, error) {
	dto := gen.Import{Id: idconv.FromPg(imp.ID), Status: gen.ImportStatus(imp.Status), CreatedAt: idconv.FromPgTimestamptz(imp.CreatedAt)}
	assetID := idconv.FromPg(imp.AssetID)
	dto.AssetId = &assetID
	if imp.SeriesID.Valid {
		seriesID := idconv.FromPg(imp.SeriesID)
		dto.SeriesId = &seriesID
	}
	if imp.Encoding != "" {
		dto.Encoding = &imp.Encoding
	}
	if imp.SplitPreset != "" {
		dto.SplitPreset = &imp.SplitPreset
	}
	if imp.ErrorMsg.Valid {
		dto.ErrorMsg = &imp.ErrorMsg.String
	}
	if len(imp.Chapters) > 0 {
		var chapters []chapterDoc
		if err := json.Unmarshal(imp.Chapters, &chapters); err != nil {
			return gen.Import{}, err
		}
		previews := make([]gen.ChapterPreview, 0, len(chapters))
		for _, c := range chapters {
			previews = append(previews, gen.ChapterPreview{Index: c.Index, Title: c.Title, WordCount: c.WordCount})
		}
		dto.Chapters = &previews
	}
	return dto, nil
}

func presetFromDTO(p gen.ImportPreviewRequestSplitPreset) importer.SplitPreset {
	switch p {
	case gen.ZhChapter:
		return importer.PresetChinese
	case gen.EnChapter:
		return importer.PresetEnglish
	case gen.ViChuong:
		return importer.PresetViet
	default:
		return importer.PresetAuto
	}
}

// importObjectOptions reads the exact object version that was size- and
// type-checked at finalize, not whatever was written to the key since.
func importObjectOptions(asset dbgen.Asset) minio.GetObjectOptions {
	return minio.GetObjectOptions{VersionID: asset.StorageVersionID.String}
}
