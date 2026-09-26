package render

import (
	"encoding/json"
	"fmt"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// SettingsFromRow reads a stored render_settings row.
func SettingsFromRow(row dbgen.RenderSetting) (Settings, error) {
	s := Settings{
		Width: int(row.Width), Height: int(row.Height), FPS: int(row.Fps), Encoder: row.Encoder, Subtitles: row.Subtitles,
		DefaultMotion: row.DefaultMotion, CrossfadeMs: int(row.CrossfadeMs),
		LoudnessLUFSx10: int(row.LoudnessLufsX10), TruePeakDBTPx10: int(row.TruePeakDbtpX10),
	}
	if err := json.Unmarshal(row.SubtitleStyle, &s.SubtitleStyle); err != nil {
		return Settings{}, fmt.Errorf("render: stored subtitle style: %w", err)
	}
	return s, s.Validate()
}

// ManifestScenes turns an episode's scenes with their selected takes
// into manifest entries. Every scene needs a selected image, a voice (it
// sets the scene's length) and an alignment (it times the subtitles);
// each scene without them, and each motion the GPU cannot render,
// becomes a reason the render cannot start. No placeholder is ever
// substituted.
func ManifestScenes(rows []dbgen.ListManifestSceneInputsRow, fps int, depth bool) ([]ManifestScene, []string) {
	if len(rows) == 0 {
		return nil, []string{"The episode has no scenes yet."}
	}
	var reasons []string
	scenes := make([]ManifestScene, 0, len(rows))
	for _, r := range rows {
		idx := int(r.Idx)
		s := ManifestScene{
			SceneID: idconv.FromPg(r.SceneID).String(), Idx: idx, Motion: r.MotionPreset,
			ImageSha256: r.ImageSha256.String, VoiceSha256: r.VoiceSha256.String,
		}
		if r.ImageAssetID.Valid {
			s.ImageAssetID = idconv.FromPg(r.ImageAssetID).String()
		} else {
			reasons = append(reasons, fmt.Sprintf("Scene %d has no selected image.", idx))
		}
		if r.VoiceAssetID.Valid && r.VoiceDurationMs.Valid {
			s.VoiceAssetID = idconv.FromPg(r.VoiceAssetID).String()
			s.DurationFrames = FramesForMs(int64(r.VoiceDurationMs.Int32), fps)
		}
		if s.DurationFrames <= 0 {
			reasons = append(reasons, fmt.Sprintf("Scene %d has no ready voice take.", idx))
		}
		if r.AlignAssetID.Valid {
			s.AlignAssetID = idconv.FromPg(r.AlignAssetID).String()
		} else {
			reasons = append(reasons, fmt.Sprintf("Scene %d has no subtitle alignment.", idx))
		}
		if ok, why := MotionAvailable(s.Motion, depth); !ok {
			reasons = append(reasons, fmt.Sprintf("Scene %d motion %s: %s.", idx, s.Motion, why))
		}
		s.ImageScore = imageScore(r.ImageParams)
		scenes = append(scenes, s)
	}
	return scenes, reasons
}

// imageScore reads the score the scoring step writes into an image
// take's params, when present.
func imageScore(params []byte) *float64 {
	var p struct {
		Score *float64 `json:"score"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return nil
	}
	return p.Score
}

// plannedStep is one step of a manifest's DAG before it has an id.
type plannedStep struct {
	Kind  string
	Input StepInput
	// CacheKind is set for steps whose output is a cache entry, which
	// freezing skips when the entry already exists.
	CacheKind string
}

// planSteps lists every cacheable step of a manifest in timeline order
// followed by the audio master and subtitles. Compose and preview are
// added by the caller: they always run.
func planSteps(m Manifest, t Timeline, manifestID, lang string) []plannedStep {
	var out []plannedStep
	for k, seg := range t.Segments {
		kind, cache := KindSceneBody, cacheBody
		if seg.Kind == SegmentTransition {
			kind, cache = KindTransition, cacheTransition
		}
		out = append(out, plannedStep{Kind: kind, CacheKind: cache, Input: StepInput{ManifestID: manifestID, Segment: k, Hash: m.SegmentHash(seg)}})
	}
	return append(out,
		plannedStep{Kind: KindAudioMaster, CacheKind: cacheAudio, Input: StepInput{ManifestID: manifestID, Hash: m.AudioHash(t)}},
		plannedStep{Kind: KindSubtitles, CacheKind: cacheSubtitles, Input: StepInput{ManifestID: manifestID, Hash: m.SubtitlesHash(t, lang)}},
	)
}

// pinnedHashes are the cache entries a manifest needs, deduplicated
// (two identical transitions share one entry).
func pinnedHashes(steps []plannedStep) []string {
	seen := make(map[string]bool, len(steps))
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		if !seen[s.Input.Hash] {
			seen[s.Input.Hash] = true
			out = append(out, s.Input.Hash)
		}
	}
	return out
}
