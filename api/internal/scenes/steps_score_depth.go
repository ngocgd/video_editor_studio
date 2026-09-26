package scenes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/vision"
	"loomtale/api/internal/storage"
)

// Step kinds and engines (manifest entry names) of the image analysis
// steps that follow a new image take.
const (
	KindScore = "image.score"
	KindDepth = "image.depth"

	ScoreEngine = "dinov2-base"
	DepthEngine = "depth-anything-v2-small"
)

// MaxScoreRefs is how many approved reference images one score compares
// against; the worker's scorer refuses more.
const MaxScoreRefs = 32

// Keys the analysis steps add to an image take's params.
const (
	ParamScore        = "score"
	ParamDepthAssetID = "depthAssetId"
)

// ScoreHandler is image.score: the character-consistency score of an
// image take (DINOv2 cosine similarity against the approved reference
// images of the characters in the scene), written to the take's
// params.score.
type ScoreHandler struct{ StepDeps }

// DepthHandler is image.depth: a depth map of an image take
// (Depth-Anything-V2-Small), stored as an image asset whose id is written
// to the take's params.depthAssetId.
type DepthHandler struct{ StepDeps }

var (
	_ pipeline.StepHandler = (*ScoreHandler)(nil)
	_ pipeline.StepHandler = (*DepthHandler)(nil)
)

func (h *ScoreHandler) Kind() string { return KindScore }
func (h *DepthHandler) Kind() string { return KindDepth }

func (h *ScoreHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (h *DepthHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (h *ScoreHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return &pipeline.ModelRef{Backend: pyworkerBackend, Model: ScoreEngine}, nil
}

func (h *DepthHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return &pipeline.ModelRef{Backend: pyworkerBackend, Model: DepthEngine}, nil
}

// InputHash of the score: the selected image take and the approved refs
// of the scene's characters, so approving a new ref makes it stale.
func (h *ScoreHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	scx, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return "", err
	}
	take, err := h.selectedImageTake(ctx, s.TenantID, scx)
	if err != nil {
		return analysisHash(KindScore, uuid.Nil), nil //nolint:nilerr // Run reports the missing take
	}
	refs, err := h.scoreRefs(ctx, s.TenantID, scx)
	if err != nil {
		return "", err
	}
	parts := make([]uuid.UUID, 0, len(refs)+1)
	parts = append(parts, idOf(take.ID))
	for _, r := range refs {
		parts = append(parts, idOf(r.AssetID))
	}
	return analysisHash(KindScore, parts...), nil
}

// InputHash of the depth map: the selected image take only.
func (h *DepthHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	scx, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return "", err
	}
	take, err := h.selectedImageTake(ctx, s.TenantID, scx)
	if err != nil {
		return analysisHash(KindDepth, uuid.Nil), nil //nolint:nilerr // Run reports the missing take
	}
	return analysisHash(KindDepth, idOf(take.ID)), nil
}

func analysisHash(kind string, ids ...uuid.UUID) string {
	s := sha256.New()
	s.Write([]byte(kind))
	for _, id := range ids {
		s.Write([]byte{0})
		s.Write([]byte(id.String()))
	}
	return hex.EncodeToString(s.Sum(nil))
}

func (d StepDeps) selectedImageTake(ctx context.Context, tenantID uuid.UUID, scx sceneContext) (dbgen.SceneTake, error) {
	return d.Service.Queries.GetSelectedTake(ctx, dbgen.GetSelectedTakeParams{
		TenantID: idconv.ToPg(tenantID), SceneID: scx.Scene.ID, Kind: TakeImage,
	})
}

// analysedTake is the take named by the step input, else the scene's
// selected image take. A named take must be an image take of this scene.
func (d StepDeps) analysedTake(ctx context.Context, sc *pipeline.StepContext, scx sceneContext) (dbgen.SceneTake, error) {
	var in AnalysisInput
	_ = sc.Input(&in) // an empty or foreign input falls back to the selected take
	var (
		take dbgen.SceneTake
		err  error
	)
	if in.TakeID != uuid.Nil {
		take, err = d.Service.Queries.GetTake(ctx, dbgen.GetTakeParams{TenantID: idconv.ToPg(sc.Tenant()), ID: idconv.ToPg(in.TakeID)})
		if err == nil && (take.Kind != TakeImage || take.SceneID != scx.Scene.ID) {
			return dbgen.SceneTake{}, fmt.Errorf("%w: take %s is not an image take of this scene", pipeline.ErrValidation, in.TakeID)
		}
	} else {
		take, err = d.selectedImageTake(ctx, sc.Tenant(), scx)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.SceneTake{}, fmt.Errorf("%w: the scene has no image take to analyse", pipeline.ErrValidation)
	}
	return take, err
}

// scoreRefs picks up to MaxScoreRefs approved reference images of the
// scene's characters, taken in turn from each character (in the scene's
// order) so every character is represented.
func (d StepDeps) scoreRefs(ctx context.Context, tenantID uuid.UUID, scx sceneContext) ([]dbgen.CharacterRef, error) {
	charIDs := scx.sceneInputs().CharacterIDs
	if len(charIDs) == 0 {
		return nil, nil
	}
	rows, err := d.Service.Queries.ListCharacterRefsBySeries(ctx, dbgen.ListCharacterRefsBySeriesParams{
		TenantID: idconv.ToPg(tenantID), SeriesID: scx.Episode.SeriesID,
	})
	if err != nil {
		return nil, err
	}
	return PickScoreRefs(charIDs, rows, MaxScoreRefs), nil
}

// PickScoreRefs returns up to limit approved refs of the given characters,
// round-robin across the characters in order, each character's refs in
// their stored order.
func PickScoreRefs(charIDs []uuid.UUID, refs []dbgen.CharacterRef, limit int) []dbgen.CharacterRef {
	byChar := map[uuid.UUID][]dbgen.CharacterRef{}
	for _, r := range refs {
		if r.Approved {
			id := idOf(r.CharacterID)
			byChar[id] = append(byChar[id], r)
		}
	}
	var out []dbgen.CharacterRef
	for round := 0; len(out) < limit; round++ {
		added := false
		for _, id := range charIDs {
			if rs := byChar[id]; round < len(rs) && len(out) < limit {
				out = append(out, rs[round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// presignAsset is a presigned GET of one of the tenant's assets.
func (d StepDeps) presignAsset(ctx context.Context, tenantID uuid.UUID, assetID pgtype.UUID) (string, error) {
	a, err := d.Service.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(tenantID), ID: assetID})
	if err != nil {
		return "", fmt.Errorf("%w: asset %s: %v", pipeline.ErrValidation, idOf(assetID), err)
	}
	return d.Storage.PresignGet(ctx, a.StorageKey, a.StorageVersionID.String, presignTTL)
}

// mergeTakeParams adds patch to the take's params.
func (d StepDeps) mergeTakeParams(ctx context.Context, tenantID uuid.UUID, take dbgen.SceneTake, patch map[string]any) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	n, err := d.Service.Queries.MergeTakeParams(ctx, dbgen.MergeTakeParamsParams{Patch: raw, TenantID: idconv.ToPg(tenantID), ID: take.ID})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: take %s no longer exists", pipeline.ErrValidation, idOf(take.ID))
	}
	return nil
}

func (h *ScoreHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	scx, err := loadSceneContext(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	take, err := h.analysedTake(ctx, sc, scx)
	if err != nil {
		return nil, err
	}
	refs, err := h.scoreRefs(ctx, sc.Tenant(), scx)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		// Nothing to be consistent with: not a failure, and no score.
		sc.Log("no approved reference images of the scene's characters: not scored")
		return pipeline.Output{"takeId": idOf(take.ID).String(), "skipped": "no approved references"}, nil
	}
	if h.Vision == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the scoring engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	imageURL, err := h.presignAsset(ctx, sc.Tenant(), take.AssetID)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(refs))
	for _, r := range refs {
		u, err := h.presignAsset(ctx, sc.Tenant(), r.AssetID)
		if err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	sc.Progress(10, 0)
	score, meta, err := h.Vision.Score(ctx, vision.ScoreRequest{
		Engine: ScoreEngine, ImageGetURL: imageURL, Params: map[string]string{"reference_urls": strings.Join(urls, "\n")},
	})
	if err != nil {
		return nil, err
	}
	patch := map[string]any{ParamScore: score, "scoreReferences": len(refs), "scoreEngine": ScoreEngine}
	for _, k := range []string{"min", "max"} {
		if v, err := strconv.ParseFloat(meta[k], 64); err == nil {
			patch["score"+strings.ToUpper(k[:1])+k[1:]] = v
		}
	}
	if err := h.mergeTakeParams(ctx, sc.Tenant(), take, patch); err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"takeId": idOf(take.ID).String(), "score": score, "references": len(refs)}, nil
}

func (h *DepthHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	scx, err := loadSceneContext(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	take, err := h.analysedTake(ctx, sc, scx)
	if err != nil {
		return nil, err
	}
	if h.Vision == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the depth engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	imageURL, err := h.presignAsset(ctx, sc.Tenant(), take.AssetID)
	if err != nil {
		return nil, err
	}
	assetID := idconv.NewV7()
	key := storage.Key(sc.Tenant().String(), "image", assetID)
	putURL, err := h.Storage.PresignPut(ctx, key, presignTTL)
	if err != nil {
		return nil, err
	}
	sc.Progress(10, 0)
	if _, err := h.Vision.Depth(ctx, vision.DepthRequest{
		Engine: DepthEngine, ImageGetURL: imageURL, OutputPutURL: putURL, Params: map[string]string{"output_key": key},
	}); err != nil {
		return nil, err
	}
	stored, err := h.Storage.Stat(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("scenes: the depth engine did not upload its map: %w", err)
	}
	asset, err := h.Service.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(assetID), TenantID: idconv.ToPg(sc.Tenant()), Kind: "image", StorageKey: key, Mime: "image/png",
		Bytes: idconv.ToPgInt8(stored.Size), StorageVersionID: idconv.ToPgText(stored.VersionID),
	})
	if err != nil {
		return nil, err
	}
	if err := h.mergeTakeParams(ctx, sc.Tenant(), take, map[string]any{ParamDepthAssetID: assetID.String(), "depthEngine": DepthEngine}); err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"takeId": idOf(take.ID).String(), "assetId": idOf(asset.ID).String()}, nil
}
