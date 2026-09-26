// Package media owns the first media derivatives: WebP/AVIF image
// variants at fixed widths and waveform peaks for audio, both produced
// by pipeline steps on the cpu queue through the shared ffmpeg runner,
// plus the HTTP routes that serve them.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig for uploaded JPEG references
	_ "image/png"  // DecodeConfig for generated and uploaded PNGs
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
)

// Step kinds this package registers.
const (
	KindVariants = "media.variants"
	KindPeaks    = "media.peaks"
	// ScopeAsset is the scope kind of both steps: scope_id is the asset.
	ScopeAsset = "asset"
)

// VariantWidths are the widths every image gets a WebP and an AVIF of.
var VariantWidths = []int{320, 640, 1280}

// Encoder settings: WebP quality and the AV1 CRF of the AVIF stills.
const (
	webpQuality = 78
	avifCRF     = 34
)

// Deps is what the media steps need at run time. Runner is nil in a
// process that only enqueues (the api), where Run refuses.
type Deps struct {
	Queries *dbgen.Queries
	Storage *storage.Internal
	Runner  *ffmpeg.Runner
}

// VariantsMap is the shape stored in assets.variants: format -> width ->
// storage key, plus "peaks" -> key for audio.
type VariantsMap struct {
	WebP  map[string]string `json:"webp,omitempty"`
	AVIF  map[string]string `json:"avif,omitempty"`
	Peaks string            `json:"peaks,omitempty"`
}

// DecodeVariants reads an assets.variants value.
func DecodeVariants(raw []byte) VariantsMap {
	var v VariantsMap
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

// VariantKey returns the storage key of a named variant ("webp-320",
// "avif-1280"), or "" when it does not exist.
func (v VariantsMap) VariantKey(name string) string {
	format, width, ok := cutVariantName(name)
	if !ok {
		return ""
	}
	switch format {
	case "webp":
		return v.WebP[width]
	case "avif":
		return v.AVIF[width]
	}
	return ""
}

func cutVariantName(name string) (string, string, bool) {
	for i := 0; i < len(name); i++ {
		if name[i] == '-' {
			return name[:i], name[i+1:], true
		}
	}
	return "", "", false
}

// VariantsHandler is the media.variants step.
type VariantsHandler struct{ Deps }

var _ pipeline.StepHandler = (*VariantsHandler)(nil)

func (h *VariantsHandler) Kind() string { return KindVariants }

func (h *VariantsHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueCPU, nil
}

// InputHash is the asset's own identity and object version: variants are
// a pure function of those bytes.
func (h *VariantsHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	return assetHash(ctx, h.Queries, s)
}

func (h *VariantsHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}

func (h *VariantsHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if h.Runner == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: ffmpeg is not configured in this process", pipeline.ErrEngineNotInstalled)
	}
	asset, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(sc.Tenant()), ID: idconv.ToPg(sc.ScopeID())})
	if err != nil {
		return nil, fmt.Errorf("%w: asset: %v", pipeline.ErrValidation, err)
	}
	format, ok := ffmpeg.FormatForMIME(asset.Mime)
	if asset.Kind != "image" || asset.Status != "ready" || !ok || format != ffmpeg.FormatImage2 {
		return nil, fmt.Errorf("%w: asset is not a ready PNG, JPEG or WebP image", pipeline.ErrValidation)
	}

	dir, err := os.MkdirTemp("", "variants-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	source := filepath.Join(dir, "source"+extensionFor(asset.Mime))
	if err := h.Storage.DownloadTo(ctx, asset.StorageKey, asset.StorageVersionID.String, source); err != nil {
		return nil, fmt.Errorf("media: download source: %w", err)
	}
	srcWidth, srcHeight := imageSize(source)

	tenantID := sc.Tenant().String()
	assetID := idconv.FromPg(asset.ID)
	out := VariantsMap{WebP: map[string]string{}, AVIF: map[string]string{}}
	total := len(VariantWidths) * 2
	done := 0
	for _, width := range VariantWidths {
		// Never upscale beyond the source, except that the smallest width
		// always exists so every tile has one.
		if srcWidth > 0 && width > srcWidth && width != VariantWidths[0] {
			continue
		}
		for _, spec := range []struct {
			format, muxer, codec, mime string
			quality                    int
			still                      bool
		}{
			{"webp", "webp", "libwebp", "image/webp", webpQuality, false},
			{"avif", "avif", "libaom-av1", "image/avif", avifCRF, true},
		} {
			name := spec.format + "-" + strconv.Itoa(width)
			target := filepath.Join(dir, name+"."+spec.format)
			if err := h.Runner.Run(ctx, ffmpeg.Job{
				TempDir: dir,
				Inputs:  []ffmpeg.Input{{Format: ffmpeg.FormatImage2, Path: source}},
				Output: ffmpeg.Output{Path: target, Muxer: spec.muxer, VideoCodec: spec.codec, ScaleWidth: width,
					Quality: spec.quality, StillPicture: spec.still, Frames: 1},
			}); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(target)
			if err != nil {
				return nil, err
			}
			key := storage.DerivedKey(tenantID, assetID, name)
			if _, err := h.Storage.PutBytes(ctx, key, data, spec.mime); err != nil {
				return nil, err
			}
			if spec.format == "webp" {
				out.WebP[strconv.Itoa(width)] = key
			} else {
				out.AVIF[strconv.Itoa(width)] = key
			}
			done++
			sc.Progress(done*100/total, 0)
		}
	}

	patch, err := json.Marshal(map[string]any{"webp": out.WebP, "avif": out.AVIF})
	if err != nil {
		return nil, err
	}
	if _, err := h.Queries.SetAssetVariants(ctx, dbgen.SetAssetVariantsParams{Variants: patch, TenantID: asset.TenantID, ID: asset.ID}); err != nil {
		return nil, err
	}
	if srcWidth > 0 {
		if err := h.Queries.SetAssetDimensions(ctx, dbgen.SetAssetDimensionsParams{
			Width: idconv.ToPgInt4(int32(srcWidth)), Height: idconv.ToPgInt4(int32(srcHeight)), DurationMs: asset.DurationMs,
			TenantID: asset.TenantID, ID: asset.ID,
		}); err != nil {
			return nil, err
		}
	}
	return pipeline.Output{"variants": len(out.WebP) + len(out.AVIF)}, nil
}

func extensionFor(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/flac":
		return ".flac"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ".bin"
	}
}

// imageSize reads PNG/JPEG dimensions without decoding pixels; 0,0 when
// the format has no stdlib decoder (WebP).
func imageSize(path string) (int, int) {
	f, err := os.Open(path) //nolint:gosec // path is inside the step's own temp dir
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func assetHash(ctx context.Context, q *dbgen.Queries, s pipeline.StepRef) (string, error) {
	asset, err := q.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: idconv.ToPg(s.TenantID), ID: idconv.ToPg(s.ScopeID)})
	if err != nil {
		return "", err
	}
	return pipeline.HashInputs(s.Kind, idconv.FromPg(asset.ID), asset.StorageVersionID.String)
}

// Handlers returns the media step handlers.
func Handlers(d Deps) []pipeline.StepHandler {
	return []pipeline.StepHandler{&VariantsHandler{d}, &PeaksHandler{d}}
}

// StepSpecsFor returns the derivative step an asset needs (variants for
// an image, peaks for audio), or nothing for other kinds.
func StepSpecsFor(asset dbgen.Asset) []pipeline.StepSpec {
	kind := ""
	switch asset.Kind {
	case "image":
		kind = KindVariants
	case "audio":
		kind = KindPeaks
	default:
		return nil
	}
	return []pipeline.StepSpec{{ID: idconv.NewV7(), Kind: kind, ScopeKind: ScopeAsset, ScopeID: idconv.FromPg(asset.ID), Priority: pipeline.PriorityScene}}
}

// EnqueueDerivatives queues the derivative steps of assets as one run.
func EnqueueDerivatives(ctx context.Context, engine *pipeline.Engine, tenantID uuid.UUID, assets []dbgen.Asset, createdBy *uuid.UUID) (uuid.UUID, int, error) {
	var steps []pipeline.StepSpec
	for _, a := range assets {
		steps = append(steps, StepSpecsFor(a)...)
	}
	if len(steps) == 0 {
		return uuid.Nil, 0, nil
	}
	runID := idconv.NewV7()
	_, err := engine.Enqueue(ctx, tenantID, pipeline.RunSpec{ID: runID, ScopeKind: ScopeAsset, ScopeID: steps[0].ScopeID, Kind: "media.derivatives", CreatedBy: createdBy, Steps: steps})
	return runID, len(steps), err
}
