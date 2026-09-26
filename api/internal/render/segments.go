package render

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
)

// segmentHandler serves render.scene_body and render.transition: one
// timeline segment each, encoded with the manifest's fixed codec
// parameters and a closed GOP so compose can join them by stream copy.
type segmentHandler struct {
	stepBase
	Deps
	kind string
}

var _ pipeline.StepHandler = (*segmentHandler)(nil)

func (h *segmentHandler) Kind() string { return h.kind }

func (h *segmentHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	j, in, err := stepJob(ctx, h.Deps, sc)
	if err != nil {
		return nil, err
	}
	if in.Segment < 0 || in.Segment >= len(j.Timeline.Segments) {
		return nil, fmt.Errorf("%w: segment %d is not on the manifest's timeline", pipeline.ErrValidation, in.Segment)
	}
	seg := j.Timeline.Segments[in.Segment]
	if (seg.Kind == SegmentBody) != (h.kind == KindSceneBody) {
		return nil, fmt.Errorf("%w: segment %d is a %s, not a %s step", pipeline.ErrValidation, in.Segment, seg.Kind, h.kind)
	}
	if hash := j.Manifest.SegmentHash(seg); hash != in.Hash {
		return nil, fmt.Errorf("%w: segment %d hash changed since the manifest was frozen", pipeline.ErrValidation, in.Segment)
	}
	c, reused, err := h.ensureSegment(ctx, sc, j, in.Segment)
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"assetId": c.AssetID.String(), "frames": c.Frames, "reused": reused}, nil
}

// ensureSegment returns timeline segment k from the cache, encoding and
// storing it first when there is no verified entry. Compose calls it
// too, so a segment that vanished or failed its checksum after its own
// step ran is re-encoded rather than joined.
func (d Deps) ensureSegment(ctx context.Context, sc *pipeline.StepContext, j *job, k int) (*cached, bool, error) {
	seg := j.Timeline.Segments[k]
	hash := j.Manifest.SegmentHash(seg)
	if c, err := d.lookup(ctx, j, hash, sc.Log); err != nil || c != nil {
		return c, c != nil, err
	}
	codec, err := d.workerCodec(ctx, j)
	if err != nil {
		return nil, false, err
	}
	dir, cleanup, err := newTempDir("render-seg-")
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	shown := []int{seg.Scene}
	if seg.Kind == SegmentTransition {
		shown = append(shown, seg.Scene+1)
	}
	var inputs []ffmpeg.Input
	for n, i := range shown {
		s := j.Manifest.Scenes[i]
		path, format, err := d.fetch(ctx, j, dir, s.ImageAssetID, "image"+strconv.Itoa(n))
		if err != nil {
			return nil, false, err
		}
		if format != ffmpeg.FormatImage2 {
			return nil, false, fmt.Errorf("%w: scene %d image is not a still image", pipeline.ErrValidation, s.Idx)
		}
		inputs = append(inputs, ffmpeg.Input{Format: format, Path: path, Loop: true, FrameRate: j.Manifest.Settings.FPS})
	}
	burn, err := d.burnFile(ctx, j, seg, dir)
	if err != nil {
		return nil, false, err
	}

	st := j.Manifest.Settings
	var graph *ffmpeg.Graph
	if seg.Kind == SegmentBody {
		graph, err = BodyGraph(st, j.Manifest.Scenes[seg.Scene].SceneMotion(), seg, burn)
	} else {
		graph, err = TransitionGraph(st, j.Manifest.Scenes[seg.Scene].SceneMotion(), j.Manifest.Scenes[seg.Scene+1].SceneMotion(), seg, burn)
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	out := filepath.Join(dir, "segment.mp4")
	durationMs := FrameMs(seg.Frames, st.FPS)
	if err := d.Runner.Run(ctx, ffmpeg.Job{
		TempDir: dir, Inputs: inputs, Graph: graph,
		Output: ffmpeg.Output{
			Path: out, Muxer: "mp4", VideoCodec: codec, Video: VideoEncode(codec, st.FPS),
			Maps: []string{OutputLabel}, Frames: seg.Frames, NoAudio: true,
		},
		TotalMs: durationMs, OnProgress: func(pct int) { sc.Progress(min(pct, 99), 0) },
	}); err != nil {
		return nil, false, err
	}
	cacheKind := cacheBody
	if seg.Kind == SegmentTransition {
		cacheKind = cacheTransition
	}
	c, err := d.store(ctx, j, storeObject{
		Hash: hash, CacheKind: cacheKind, AssetKind: "video", Path: out, Ext: ".mp4", Mime: "video/mp4",
		Frames: seg.Frames, DurationMs: durationMs,
	}, sc.Log)
	return c, false, err
}

// burnFile writes the ASS file of the cues burned into seg, or returns
// nil when subtitles are not burned or the segment carries none.
func (d Deps) burnFile(ctx context.Context, j *job, seg Segment, dir string) (*Burn, error) {
	st := j.Manifest.Settings
	if !st.Burn() {
		return nil, nil
	}
	local := map[int][]Cue{}
	for _, i := range []int{seg.Scene, seg.Scene + 1} {
		if i == seg.Scene+1 && seg.Kind != SegmentTransition {
			break
		}
		cues, err := d.localCues(ctx, j, i)
		if err != nil {
			return nil, err
		}
		local[i] = cues
	}
	cues := SegmentCues(j.Timeline, seg, func(i int) []Cue { return local[i] })
	if len(cues) == 0 {
		return nil, nil
	}
	const name = "burn.ass"
	if err := writeFile(filepath.Join(dir, name), []byte(ASS(cues, st.SubtitleStyle, st.Width, st.Height))); err != nil {
		return nil, err
	}
	return &Burn{File: name, FontsDir: d.fontsDir()}, nil
}

// workerCodec is the manifest's encoder, refused when this worker cannot
// produce it: segments of different encoders cannot be joined by copy.
func (d Deps) workerCodec(ctx context.Context, j *job) (string, error) {
	codec := j.Manifest.Settings.Encoder
	switch codec {
	case EncoderX264:
		return codec, nil
	case EncoderNVENC:
		if d.Encoder == nil || !d.Encoder.Info(ctx).NVENC {
			return "", fmt.Errorf("%w: the render was frozen for h264_nvenc but this worker has no NVENC", pipeline.ErrEngineNotInstalled)
		}
		return codec, nil
	default:
		return "", fmt.Errorf("%w: manifest encoder %q is not a concrete encoder", pipeline.ErrValidation, codec)
	}
}
