package render

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
)

// audioBitrateK is the narration master's AAC bitrate.
const audioBitrateK = 192

// AudioHandler is render.audio_master: the manifest's voice takes laid
// end to end on the frame grid, two-pass loudness-normalised, AAC.
type AudioHandler struct {
	stepBase
	Deps
}

var _ pipeline.StepHandler = (*AudioHandler)(nil)

func (h *AudioHandler) Kind() string { return KindAudioMaster }

func (h *AudioHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	j, in, err := stepJob(ctx, h.Deps, sc)
	if err != nil {
		return nil, err
	}
	if j.Manifest.AudioHash(j.Timeline) != in.Hash {
		return nil, fmt.Errorf("%w: audio hash changed since the manifest was frozen", pipeline.ErrValidation)
	}
	c, reused, err := h.ensureAudio(ctx, sc, j)
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{"assetId": c.AssetID.String(), "reused": reused}, nil
}

// ensureAudio returns the narration master from the cache, building it
// first when there is no verified entry.
func (d Deps) ensureAudio(ctx context.Context, sc *pipeline.StepContext, j *job) (*cached, bool, error) {
	hash := j.Manifest.AudioHash(j.Timeline)
	if c, err := d.lookup(ctx, j, hash, sc.Log); err != nil || c != nil {
		return c, c != nil, err
	}
	dir, cleanup, err := newTempDir("render-audio-")
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	inputs := make([]ffmpeg.Input, len(j.Manifest.Scenes))
	for i, s := range j.Manifest.Scenes {
		if s.VoiceAssetID == "" {
			return nil, false, fmt.Errorf("%w: scene %d has no voice take", pipeline.ErrValidation, s.Idx)
		}
		path, format, err := d.fetch(ctx, j, dir, s.VoiceAssetID, "voice"+strconv.Itoa(i))
		if err != nil {
			return nil, false, err
		}
		inputs[i] = ffmpeg.Input{Format: format, Path: path}
	}
	st, tl := j.Manifest.Settings, j.Timeline
	totalMs := tl.DurationMs()

	measure, err := AudioGraph(st, tl, nil)
	if err != nil {
		return nil, false, err
	}
	log, err := d.Runner.RunLog(ctx, ffmpeg.Job{
		TempDir: dir, Inputs: inputs, Graph: measure,
		Output: ffmpeg.Output{Muxer: ffmpeg.MuxerNull, Maps: []string{AudioLabel}, NoVideo: true},
	})
	if err != nil {
		return nil, false, err
	}
	stats, err := ParseLoudnorm(log)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	sc.Progress(40, 0)

	normalise, err := AudioGraph(st, tl, &stats)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	out := filepath.Join(dir, "narration.m4a")
	if err := d.Runner.Run(ctx, ffmpeg.Job{
		TempDir: dir, Inputs: inputs, Graph: normalise,
		Output: ffmpeg.Output{
			Path: out, Muxer: "mp4", AudioCodec: "aac", AudioBitrateK: audioBitrateK,
			SampleRate: AudioSampleRate, Maps: []string{AudioLabel}, NoVideo: true,
		},
		TotalMs: totalMs, OnProgress: func(pct int) { sc.Progress(40+min(pct, 99)*59/100, 0) },
	}); err != nil {
		return nil, false, err
	}
	c, err := d.store(ctx, j, storeObject{
		Hash: hash, CacheKind: cacheAudio, AssetKind: "audio", Path: out, Ext: ".m4a", Mime: "audio/mp4",
		Frames: tl.TotalFrames, DurationMs: totalMs,
	}, sc.Log)
	return c, false, err
}
