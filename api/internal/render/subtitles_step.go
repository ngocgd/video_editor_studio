package render

import (
	"context"
	"fmt"
	"path/filepath"

	"loomtale/api/internal/pipeline"
)

// srtMime is the type the episode subtitle file is stored with.
const srtMime = "application/x-subrip"

// SubtitlesHandler is render.subtitles: the episode SRT built from the
// manifest's alignment takes, each scene's cues offset by its start on
// the timeline. It is muxed as a soft subtitle stream and kept as the
// sidecar file publishing uploads.
type SubtitlesHandler struct {
	stepBase
	Deps
}

var _ pipeline.StepHandler = (*SubtitlesHandler)(nil)

func (h *SubtitlesHandler) Kind() string { return KindSubtitles }

func (h *SubtitlesHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	j, in, err := stepJob(ctx, h.Deps, sc)
	if err != nil {
		return nil, err
	}
	if j.Manifest.SubtitlesHash(j.Timeline, j.Lang) != in.Hash {
		return nil, fmt.Errorf("%w: subtitles hash changed since the manifest was frozen", pipeline.ErrValidation)
	}
	c, reused, err := h.ensureSubtitles(ctx, sc, j)
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"assetId": c.AssetID.String(), "reused": reused}, nil
}

// episodeCues are every scene's cues on the episode clock.
func (d Deps) episodeCues(ctx context.Context, j *job) ([]Cue, error) {
	var all []Cue
	for i := range j.Manifest.Scenes {
		doc, err := d.alignDoc(ctx, j, i)
		if err != nil {
			return nil, err
		}
		span := j.Timeline.Scenes[i]
		all = append(all, SceneCues(doc, FrameMs(span.StartFrame, j.Timeline.FPS), FrameMs(span.Frames, j.Timeline.FPS))...)
	}
	return all, nil
}

// ensureSubtitles returns the episode SRT from the cache, building it
// first when there is no verified entry.
func (d Deps) ensureSubtitles(ctx context.Context, sc *pipeline.StepContext, j *job) (*cached, bool, error) {
	hash := j.Manifest.SubtitlesHash(j.Timeline, j.Lang)
	if c, err := d.lookup(ctx, j, hash, sc.Log); err != nil || c != nil {
		return c, c != nil, err
	}
	cues, err := d.episodeCues(ctx, j)
	if err != nil {
		return nil, false, err
	}
	dir, cleanup, err := newTempDir("render-subs-")
	if err != nil {
		return nil, false, err
	}
	defer cleanup()
	out := filepath.Join(dir, "episode.srt")
	if err := writeFile(out, []byte(SRT(cues))); err != nil {
		return nil, false, err
	}
	c, err := d.store(ctx, j, storeObject{
		Hash: hash, CacheKind: cacheSubtitles, AssetKind: "document", Path: out, Ext: ".srt", Mime: srtMime,
		DurationMs: j.Timeline.DurationMs(),
	}, sc.Log)
	return c, false, err
}
