package scenes

import (
	"context"
	"fmt"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/storage"
)

// AlignHandler is align.subtitles: the selected voice take is aligned to
// the scene's narration; the subtitle cues JSON becomes an align take.
type AlignHandler struct{ StepDeps }

var _ pipeline.StepHandler = (*AlignHandler)(nil)

func (h *AlignHandler) Kind() string { return KindAlign }

func (h *AlignHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (h *AlignHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	sc, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return "", err
	}
	return AlignComponents(sc.Inputs, sc.sceneInputs()).Hash(), nil
}

func (h *AlignHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return &pipeline.ModelRef{Backend: pyworkerBackend, Model: AlignEngine}, nil
}

func (h *AlignHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	scx, err := loadSceneContext(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	if scx.VoiceTake == nil {
		return nil, fmt.Errorf("%w: the scene has no voice take to align", pipeline.ErrValidation)
	}
	if h.Align == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the alignment engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	in := scx.sceneInputs()
	tid := idconv.ToPg(sc.Tenant())
	voice, err := h.Service.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: tid, ID: scx.VoiceTake.AssetID})
	if err != nil {
		return nil, fmt.Errorf("%w: voice asset: %v", pipeline.ErrValidation, err)
	}
	audioURL, err := h.Storage.PresignGet(ctx, voice.StorageKey, voice.StorageVersionID.String, presignTTL)
	if err != nil {
		return nil, err
	}
	assetID := idconv.NewV7()
	key := storage.Key(sc.Tenant().String(), "document", assetID)
	putURL, err := h.Storage.PresignPut(ctx, key, presignTTL)
	if err != nil {
		return nil, err
	}
	res, err := h.Align.Align(ctx, align.Request{
		Engine: AlignEngine, AudioGetURL: audioURL, Text: SegmentsText(in.Segments), OutputPutURL: putURL,
		Params: map[string]string{"language": scx.Inputs.Lang, "output_key": key},
	}, func(pct, eta int) { sc.Progress(pct*95/100, eta) })
	if err != nil {
		return nil, err
	}
	stored, err := h.Storage.Stat(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("scenes: the aligner did not upload its cues: %w", err)
	}
	asset, err := h.Service.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(assetID), TenantID: tid, Kind: "document", StorageKey: key, Mime: "application/json",
		Bytes: idconv.ToPgInt8(stored.Size), StorageVersionID: idconv.ToPgText(stored.VersionID),
	})
	if err != nil {
		return nil, err
	}
	take, err := h.Service.RecordTake(ctx, sc.Tenant(), in.ID, TakeAlign, idOf(asset.ID), AlignComponents(scx.Inputs, in),
		map[string]any{"segments": res.SegmentCount, "metadata": res.Metadata}, sc.StepID(), sc.RunID())
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"assetId": assetID.String(), "takeId": idOf(take.ID).String(), "cues": res.SegmentCount}, nil
}
