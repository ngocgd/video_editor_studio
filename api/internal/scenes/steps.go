package scenes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"image"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/providers/vision"
	"loomtale/api/internal/storage"
)

// Engine names the voice and align steps load on the Python worker.
const AlignEngine = "whisper-align"

// presignTTL bounds the internal URLs a step hands the Python worker: the
// step's own run time plus the 10 minute margin the workers add.
const presignTTL = 40 * time.Minute

// StepDeps is what the per-scene steps need. In the api process only
// Service and the LLM registry are set (it enqueues but never runs gpu
// steps); a nil engine client makes Run fail with engine_not_installed.
type StepDeps struct {
	Service *Service
	Storage *storage.Internal
	Comfy   *comfyui.Engine
	TTS     *tts.Client
	Align   *align.Client
	Vision  *vision.Client
	Runner  *ffmpeg.Runner
	LLM     *registry.Registry
	// SceneWorkflow maps a scene model (manifest entry) to its txt2img
	// workflow; ok is false for a model without one.
	SceneWorkflow func(model string) (string, bool)
	// VisionInstalled reports whether a vision model (manifest entry) is
	// installed and may load; nil means none is, so no image take queues
	// a score or depth step.
	VisionInstalled func(ctx context.Context, model string) error
}

// Handlers returns every scene step handler.
func Handlers(d StepDeps) []pipeline.StepHandler {
	return []pipeline.StepHandler{&ImageHandler{d}, &VoiceHandler{d}, &AlignHandler{d}, &SplitHandler{d}, &ScoreHandler{d}, &DepthHandler{d}}
}

// Estimate is the per-kind duration estimate the engine chunks batches
// by, so one chunk of scene images is about ten minutes of GPU time.
func Estimate(kind string) time.Duration {
	switch kind {
	case KindImage:
		return 20 * time.Second
	case KindVoice:
		return 30 * time.Second
	case KindAlign:
		return 10 * time.Second
	case KindScore, KindDepth, media.KindVariants, media.KindPeaks:
		return 5 * time.Second
	default:
		return pipeline.DefaultStepEstimate(kind)
	}
}

func stepContext(ctx context.Context, d StepDeps, s pipeline.StepRef) (sceneContext, error) {
	return loadSceneContext(ctx, d.Service.Queries, s.TenantID, s.ScopeID)
}

// newSeed is a random ComfyUI seed, recorded on the take.
func newSeed() int64 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint32(b[:]))
}

// storeDerived writes data as a new ready asset of the tenant.
func storeDerived(ctx context.Context, d StepDeps, tenantID uuid.UUID, kind, mime string, data []byte, durationMs int) (dbgen.Asset, error) {
	assetID := idconv.NewV7()
	key := storage.Key(tenantID.String(), kind, assetID)
	stored, err := d.Storage.PutBytes(ctx, key, data, mime)
	if err != nil {
		return dbgen.Asset{}, err
	}
	params := dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(assetID), TenantID: idconv.ToPg(tenantID), Kind: kind, StorageKey: key, Mime: mime,
		Bytes: idconv.ToPgInt8(stored.Size), Sha256: idconv.ToPgText(stored.SHA256Hex), StorageVersionID: idconv.ToPgText(stored.VersionID),
	}
	if kind == "image" {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			params.Width, params.Height = idconv.ToPgInt4(int32(cfg.Width)), idconv.ToPgInt4(int32(cfg.Height))
		}
	}
	if durationMs > 0 {
		params.DurationMs = idconv.ToPgInt4(int32(durationMs))
	}
	return d.Service.Queries.CreateDerivedAsset(ctx, params)
}

// enqueueDerivatives queues the variants/peaks of a new asset; a failure
// is logged by the caller's step, not fatal: the take already exists and
// a backfill can produce the derivative later.
func enqueueDerivatives(ctx context.Context, d StepDeps, sc *pipeline.StepContext, asset dbgen.Asset) {
	if d.Service.Engine == nil {
		return
	}
	if _, _, err := media.EnqueueDerivatives(ctx, d.Service.Engine, sc.Tenant(), []dbgen.Asset{asset}, nil); err != nil {
		sc.Log(fmt.Sprintf("queueing derivatives failed: %v", err))
	}
}
