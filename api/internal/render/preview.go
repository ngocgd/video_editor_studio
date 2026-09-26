package render

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
)

// Preview proxies are 540 lines high with a small AAC track.
const (
	previewHeight   = 540
	previewBitrateK = 96
)

// PreviewHandler is render.preview: a 540p proxy of the whole episode
// and one per scene, played in the browser through short-lived presigned
// GETs with range requests.
type PreviewHandler struct {
	stepBase
	Deps
}

var _ pipeline.StepHandler = (*PreviewHandler)(nil)

func (h *PreviewHandler) Kind() string { return KindPreview }

// PreviewHash is the preview step's input hash: the proxies are a pure
// function of the manifest.
func PreviewHash(manifestHash string) string {
	return hashOf(struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		M    string `json:"m"`
	}{formatVersion, "preview", manifestHash})
}

// previewWidth keeps the frame's aspect at previewHeight, rounded to an
// even width.
func previewWidth(s Settings) int {
	return (s.Width*previewHeight/s.Height + 1) &^ 1
}

func (h *PreviewHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	j, in, err := stepJob(ctx, h.Deps, sc)
	if err != nil {
		return nil, err
	}
	if in.Hash != PreviewHash(j.Row.Hash) {
		return nil, fmt.Errorf("%w: preview input is not the manifest's preview hash", pipeline.ErrValidation)
	}
	r, err := h.Queries.GetRenderByManifest(ctx, dbgen.GetRenderByManifestParams{TenantID: idconv.ToPg(j.Tenant), ManifestID: j.Row.ID})
	if err != nil {
		return nil, fmt.Errorf("%w: the render of this manifest is missing: %v", pipeline.ErrValidation, err)
	}
	final, err := h.Queries.GetAssetByID(ctx, dbgen.GetAssetByIDParams{TenantID: r.TenantID, ID: r.AssetID})
	if err != nil {
		return nil, err
	}
	dir, cleanup, err := newTempDir("render-preview-")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	episode := filepath.Join(dir, "episode.mp4")
	if err := h.Storage.DownloadTo(ctx, final.StorageKey, final.StorageVersionID.String, episode); err != nil {
		return nil, fmt.Errorf("render: download episode: %w", err)
	}
	st := j.Manifest.Settings
	proxy := filepath.Join(dir, "preview.mp4")
	whole, err := h.lookup(ctx, j, in.Hash, sc.Log)
	if err != nil {
		return nil, err
	}
	if whole != nil {
		if _, err := h.fetchCached(ctx, whole, dir, "preview.mp4"); err != nil {
			return nil, err
		}
	} else {
		if err := h.Runner.Run(ctx, ffmpeg.Job{
			TempDir: dir, Inputs: []ffmpeg.Input{{Format: ffmpeg.FormatMP4, Path: episode}},
			Output:  h.previewOutput(proxy, st, []string{"0:v", "0:a"}, true),
			TotalMs: j.Timeline.DurationMs(), OnProgress: func(pct int) { sc.Progress(min(pct, 99)/2, 0) },
		}); err != nil {
			return nil, err
		}
		if whole, err = h.store(ctx, j, storeObject{
			Hash: in.Hash, CacheKind: cachePreview, AssetKind: "video", Path: proxy, Ext: ".mp4", Mime: "video/mp4",
			Frames: j.Timeline.TotalFrames, DurationMs: j.Timeline.DurationMs(),
		}, sc.Log); err != nil {
			return nil, err
		}
	}

	scenes := make(map[string]string, len(j.Manifest.Scenes))
	for i, s := range j.Manifest.Scenes {
		c, err := h.scenePreview(ctx, sc, j, dir, proxy, i)
		if err != nil {
			return nil, err
		}
		scenes[s.SceneID] = c.AssetID.String()
		sc.Progress(50+(i+1)*49/len(j.Manifest.Scenes), 0)
	}
	raw, err := json.Marshal(scenes)
	if err != nil {
		return nil, err
	}
	if _, err := h.Queries.SetRenderPreviews(ctx, dbgen.SetRenderPreviewsParams{
		PreviewAssetID: idconv.ToPg(whole.AssetID), ScenePreviews: raw, TenantID: r.TenantID, ID: r.ID,
	}); err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"previewAssetId": whole.AssetID.String(), "scenes": len(scenes)}, nil
}

func (h *PreviewHandler) previewOutput(path string, st Settings, maps []string, scale bool) ffmpeg.Output {
	out := ffmpeg.Output{
		Path: path, Muxer: "mp4", VideoCodec: ffmpeg.CodecX264, Video: PreviewEncode(st.FPS),
		AudioCodec: "aac", AudioBitrateK: previewBitrateK, Maps: maps, Faststart: true,
	}
	if scale {
		out.ScaleWidth = previewWidth(st)
	}
	return out
}

// scenePreview cuts scene i's span out of the episode proxy (frames and
// samples on the timeline's grid).
func (h *PreviewHandler) scenePreview(ctx context.Context, sc *pipeline.StepContext, j *job, dir, proxy string, i int) (*cached, error) {
	span := j.Timeline.Scenes[i]
	hash := hashOf(struct {
		Preview string    `json:"preview"`
		Scene   int       `json:"scene"`
		Span    SceneSpan `json:"span"`
	}{PreviewHash(j.Row.Hash), i, span})
	if c, err := h.lookup(ctx, j, hash, sc.Log); err != nil || c != nil {
		return c, err
	}
	fps := j.Timeline.FPS
	start, end := span.StartFrame, span.StartFrame+span.Frames
	graph := &ffmpeg.Graph{Chains: []ffmpeg.Chain{
		{In: []string{"0:v"}, Filters: []ffmpeg.Filter{
			ffmpeg.F("trim", ffmpeg.O("start_frame", ffmpeg.Int(start)), ffmpeg.O("end_frame", ffmpeg.Int(end))),
			ffmpeg.F("setpts", ffmpeg.O("expr", ffmpeg.Expr("PTS-STARTPTS"))),
		}, Out: []string{"v"}},
		{In: []string{"0:a"}, Filters: []ffmpeg.Filter{
			ffmpeg.F("atrim", ffmpeg.O("start_sample", ffmpeg.Int(SamplesForFrames(start, fps))), ffmpeg.O("end_sample", ffmpeg.Int(SamplesForFrames(end, fps)))),
			ffmpeg.F("asetpts", ffmpeg.O("expr", ffmpeg.Expr("PTS-STARTPTS"))),
		}, Out: []string{"a"}},
	}}
	out := filepath.Join(dir, "scene"+strconv.Itoa(i)+".mp4")
	if err := h.Runner.Run(ctx, ffmpeg.Job{
		TempDir: dir, Inputs: []ffmpeg.Input{{Format: ffmpeg.FormatMP4, Path: proxy}}, Graph: graph,
		Output: h.previewOutput(out, j.Manifest.Settings, []string{"v", "a"}, false),
	}); err != nil {
		return nil, err
	}
	return h.store(ctx, j, storeObject{
		Hash: hash, CacheKind: cachePreview, AssetKind: "video", Path: out, Ext: ".mp4", Mime: "video/mp4",
		Frames: span.Frames, DurationMs: FrameMs(span.Frames, fps),
	}, sc.Log)
}
