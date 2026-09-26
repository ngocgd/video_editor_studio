package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/media/ffmpeg"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/storage"
)

// subtitleLanguage is the ISO 639-2 tag of the soft subtitle stream.
var subtitleLanguage = map[string]string{"en": "eng", "vi": "vie"}

// ComposeHandler is render.compose: it joins the cached segments by
// stream copy (concat demuxer, local file names only), muxes the
// narration master and the soft subtitles, checks the result and records
// the render with its QC report and sha256.
type ComposeHandler struct {
	stepBase
	Deps
}

var _ pipeline.StepHandler = (*ComposeHandler)(nil)

func (h *ComposeHandler) Kind() string { return KindCompose }

func (h *ComposeHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	if err := h.ready(); err != nil {
		return nil, err
	}
	j, in, err := stepJob(ctx, h.Deps, sc)
	if err != nil {
		return nil, err
	}
	if in.Hash != j.Row.Hash {
		return nil, fmt.Errorf("%w: compose input is not the manifest hash", pipeline.ErrValidation)
	}
	// A resumed compose whose previous attempt already recorded the
	// render is done: the manifest (and so the file) cannot have changed.
	if r, err := h.Queries.GetRenderByManifest(ctx, dbgen.GetRenderByManifestParams{TenantID: idconv.ToPg(j.Tenant), ManifestID: j.Row.ID}); err == nil {
		return pipeline.Output{"renderId": idconv.FromPg(r.ID).String(), "reused": true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	dir, cleanup, err := newTempDir("render-compose-")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	inputs, maps, srt, muxSubs, err := h.gather(ctx, sc, j, dir)
	if err != nil {
		return nil, err
	}
	st := j.Manifest.Settings
	out := filepath.Join(dir, "episode.mp4")
	output := ffmpeg.Output{
		Path: out, Muxer: "mp4", VideoCodec: ffmpeg.CodecCopy, AudioCodec: ffmpeg.CodecCopy, Maps: maps, Faststart: true,
	}
	if muxSubs {
		output.SubtitleCodec, output.SubtitleLanguage = "mov_text", subtitleLanguage[j.Lang]
	}
	if err := h.Runner.Run(ctx, ffmpeg.Job{TempDir: dir, Inputs: inputs, Output: output}); err != nil {
		return nil, err
	}
	sc.Progress(80, 0)

	measured, err := h.measure(ctx, dir, out, muxSubs)
	if err != nil {
		return nil, err
	}
	report := evaluate(j.Manifest, j.Timeline, measured)
	key := storage.RenderKey(j.Tenant.String(), j.Episode.String()+"/"+j.Lang+"/episode/"+idconv.NewV7().String()+".mp4")
	stored, err := h.Storage.PutFileChecksummed(ctx, key, out, "video/mp4")
	if err != nil {
		return nil, err
	}
	report.SHA256 = stored.SHA256Hex
	asset, err := h.Queries.CreateDerivedAsset(ctx, dbgen.CreateDerivedAssetParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(j.Tenant), Kind: "video", StorageKey: key, Mime: "video/mp4",
		Bytes: idconv.ToPgInt8(stored.Size), Sha256: idconv.ToPgText(stored.SHA256Hex), StorageVersionID: idconv.ToPgText(stored.VersionID),
		Width: idconv.ToPgInt4(int32(st.Width)), Height: idconv.ToPgInt4(int32(st.Height)), DurationMs: idconv.ToPgInt4(int32(report.DurationMs)),
	})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	params := dbgen.InsertRenderParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(j.Tenant), EpisodeID: j.Row.EpisodeID, Lang: j.Lang,
		ManifestID: j.Row.ID, SettingsHash: j.Row.SettingsHash, AssetID: asset.ID, Sha256: stored.SHA256Hex,
		DurationMs: int32(report.DurationMs), Encoder: st.Encoder, Report: raw,
	}
	if srt != nil {
		params.SrtAssetID = idconv.ToPg(srt.AssetID)
	}
	r, err := h.Queries.InsertRender(ctx, params)
	if err != nil {
		return nil, err
	}
	if !report.Passed {
		sc.Log(fmt.Sprintf("render QC failed: %v", report.Failures))
	}
	sc.Progress(100, 0)
	return pipeline.Output{"renderId": idconv.FromPg(r.ID).String(), "passed": report.Passed, "sha256": stored.SHA256Hex}, nil
}

// gather makes sure every segment, the narration master and the
// subtitles exist and verify (re-encoding any that do not), downloads
// them into dir and returns compose's inputs and stream maps, the
// sidecar SRT (nil when it holds no cue) and whether it is muxed as a
// soft subtitle stream.
func (d Deps) gather(ctx context.Context, sc *pipeline.StepContext, j *job, dir string) (inputs []ffmpeg.Input, maps []string, srt *cached, muxSubs bool, err error) {
	names := make([]string, len(j.Timeline.Segments))
	for k := range j.Timeline.Segments {
		c, _, err := d.ensureSegment(ctx, sc, j, k)
		if err != nil {
			return nil, nil, nil, false, err
		}
		names[k] = fmt.Sprintf("seg%05d.mp4", k)
		if _, err := d.fetchCached(ctx, c, dir, names[k]); err != nil {
			return nil, nil, nil, false, err
		}
		sc.Progress((k+1)*60/len(names), 0)
	}
	list, err := ffmpeg.WriteConcatList(dir, "segments.txt", names, writeFile)
	if err != nil {
		return nil, nil, nil, false, err
	}
	audio, _, err := d.ensureAudio(ctx, sc, j)
	if err != nil {
		return nil, nil, nil, false, err
	}
	audioPath, err := d.fetchCached(ctx, audio, dir, "narration.m4a")
	if err != nil {
		return nil, nil, nil, false, err
	}
	inputs = []ffmpeg.Input{{Format: ffmpeg.FormatConcat, Path: list}, {Format: ffmpeg.FormatMP4, Path: audioPath}}
	maps = []string{"0:v", "1:a"}

	// The SRT is always built (publishing uploads it as captions); it is
	// muxed only in srt/both mode and when it holds at least one cue.
	srt, _, err = d.ensureSubtitles(ctx, sc, j)
	if err != nil {
		return nil, nil, nil, false, err
	}
	srtPath, err := d.fetchCached(ctx, srt, dir, "episode.srt")
	if err != nil {
		return nil, nil, nil, false, err
	}
	if info, err := os.Stat(srtPath); err != nil || info.Size() == 0 {
		return inputs, maps, nil, false, err
	}
	if !j.Manifest.Settings.SoftSubtitles() {
		return inputs, maps, srt, false, nil
	}
	return append(inputs, ffmpeg.Input{Format: ffmpeg.FormatSRT, Path: srtPath}), append(maps, "2:s"), srt, true, nil
}
