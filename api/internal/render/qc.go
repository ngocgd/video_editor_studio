package render

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"loomtale/api/internal/media/ffmpeg"
)

// QCReport is the quality report stored with a render (renders.report)
// and read by publishing. Passed is false when any check failed; the
// render is still kept so the owner can inspect it.
type QCReport struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`

	IntegratedLUFS float64 `json:"integratedLufs"`
	TruePeakDBTP   float64 `json:"truePeakDbtp"`
	TargetLUFS     float64 `json:"targetLufs"`
	TargetTruePeak float64 `json:"targetTruePeakDbtp"`

	DurationMs         int64 `json:"durationMs"`
	ExpectedDurationMs int64 `json:"expectedDurationMs"`
	// AVDriftMs is how far the audio stream's length is from the video's.
	AVDriftMs int64 `json:"avDriftMs"`
	// MaxSubtitleDriftMs bounds how far a cue can sit from its speech:
	// cues are timed on the narration's frame grid, so the bound is the
	// A/V drift plus half a frame of rounding.
	MaxSubtitleDriftMs int64 `json:"maxSubtitleDriftMs"`

	MissingScenes     []int `json:"missingScenes"`
	PlaceholderScenes []int `json:"placeholderScenes"`

	Streams struct {
		Video    int `json:"video"`
		Audio    int `json:"audio"`
		Subtitle int `json:"subtitle"`
	} `json:"streams"`
	// MissingKeyframes lists segments (timeline order) that do not start
	// on a keyframe, which would break a copy-joined boundary.
	MissingKeyframes []int `json:"missingKeyframes"`

	SceneScores []SceneScore `json:"sceneScores"`
	Encoder     string       `json:"encoder"`
	SHA256      string       `json:"sha256"`
	// ScenePreviews maps scene id to its preview proxy asset (set by the
	// preview step).
	ScenePreviews map[string]string `json:"scenePreviews,omitempty"`
}

// SceneScore is a scene's image score, when the scoring step set one.
type SceneScore struct {
	SceneID string   `json:"sceneId"`
	Idx     int      `json:"idx"`
	Score   *float64 `json:"score"`
}

// qcInputs is what the checks measured on the finished file.
type qcInputs struct {
	Probe     *ffmpeg.ProbeResult
	Keyframes []float64
	Loudness  Loudness
	SoftSubs  bool
}

// evaluate fills and judges the report from the measurements.
func evaluate(m Manifest, t Timeline, in qcInputs) QCReport {
	st := m.Settings
	r := QCReport{
		IntegratedLUFS: in.Loudness.IntegratedLUFS, TruePeakDBTP: in.Loudness.TruePeakDBTP,
		TargetLUFS: float64(st.LoudnessLUFSx10) / 10, TargetTruePeak: float64(st.TruePeakDBTPx10) / 10,
		ExpectedDurationMs: t.DurationMs(), Encoder: st.Encoder,
		MissingScenes: []int{}, PlaceholderScenes: []int{}, MissingKeyframes: []int{}, SceneScores: []SceneScore{},
		Failures: []string{},
	}
	frameMs := int64(math.Ceil(1000 / float64(t.FPS)))
	var videoMs, audioMs int64
	for _, s := range in.Probe.Streams {
		switch s.CodecType {
		case "video":
			r.Streams.Video++
			videoMs = secondsMs(s.Duration)
		case "audio":
			r.Streams.Audio++
			audioMs = secondsMs(s.Duration)
		case "subtitle":
			r.Streams.Subtitle++
		}
	}
	r.DurationMs = videoMs
	r.AVDriftMs = abs64(audioMs - videoMs)
	r.MaxSubtitleDriftMs = r.AVDriftMs + frameMs/2
	for _, s := range m.Scenes {
		if s.ImageAssetID == "" || s.VoiceAssetID == "" {
			r.MissingScenes = append(r.MissingScenes, s.Idx)
		}
		if s.Placeholder {
			r.PlaceholderScenes = append(r.PlaceholderScenes, s.Idx)
		}
		r.SceneScores = append(r.SceneScores, SceneScore{SceneID: s.SceneID, Idx: s.Idx, Score: s.ImageScore})
	}
	for k, seg := range t.Segments {
		if !hasKeyframeAt(in.Keyframes, float64(seg.StartFrame)/float64(t.FPS), 0.5/float64(t.FPS)) {
			r.MissingKeyframes = append(r.MissingKeyframes, k)
		}
	}

	fail := func(format string, args ...any) { r.Failures = append(r.Failures, fmt.Sprintf(format, args...)) }
	wantSubs := 0
	if in.SoftSubs {
		wantSubs = 1
	}
	if r.Streams.Video != 1 || r.Streams.Audio != 1 || r.Streams.Subtitle != wantSubs {
		fail("streams: %d video, %d audio, %d subtitle (want 1, 1, %d)", r.Streams.Video, r.Streams.Audio, r.Streams.Subtitle, wantSubs)
	}
	if abs64(r.DurationMs-r.ExpectedDurationMs) > frameMs {
		fail("duration %dms is more than a frame from the timeline's %dms", r.DurationMs, r.ExpectedDurationMs)
	}
	if r.AVDriftMs > 2*frameMs {
		fail("audio and video lengths differ by %dms", r.AVDriftMs)
	}
	if math.Abs(r.IntegratedLUFS-r.TargetLUFS) > 1 {
		fail("integrated loudness %.1f LUFS is more than 1 LU from %.1f", r.IntegratedLUFS, r.TargetLUFS)
	}
	if r.TruePeakDBTP > r.TargetTruePeak+0.5 {
		fail("true peak %.1f dBTP is above %.1f", r.TruePeakDBTP, r.TargetTruePeak)
	}
	if len(r.MissingScenes) > 0 || len(r.PlaceholderScenes) > 0 {
		fail("%d scenes are missing and %d are placeholders", len(r.MissingScenes), len(r.PlaceholderScenes))
	}
	if len(r.MissingKeyframes) > 0 {
		fail("%d segments do not start on a keyframe", len(r.MissingKeyframes))
	}
	r.Passed = len(r.Failures) == 0
	return r
}

func hasKeyframeAt(times []float64, at, tolerance float64) bool {
	for _, t := range times {
		if math.Abs(t-at) <= tolerance {
			return true
		}
	}
	return false
}

func secondsMs(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f*1000 + 0.5)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// measure probes the finished episode file: streams, keyframes and the
// integrated loudness and true peak of its audio.
func (d Deps) measure(ctx context.Context, dir, path string, softSubs bool) (qcInputs, error) {
	in := qcInputs{SoftSubs: softSubs}
	var err error
	if in.Probe, err = d.Prober.Probe(ctx, dir, path, ffmpeg.FormatMP4); err != nil {
		return in, err
	}
	if in.Keyframes, err = d.Prober.KeyframeTimes(ctx, dir, path); err != nil {
		return in, err
	}
	log, err := d.Runner.RunLog(ctx, ffmpeg.Job{
		TempDir: dir, Inputs: []ffmpeg.Input{{Format: ffmpeg.FormatMP4, Path: path}},
		Graph: &ffmpeg.Graph{Chains: []ffmpeg.Chain{{
			In: []string{"0:a"}, Filters: []ffmpeg.Filter{ffmpeg.F("ebur128", ffmpeg.O("peak", ffmpeg.Enum("true")))}, Out: []string{"m"},
		}}},
		Output: ffmpeg.Output{Muxer: ffmpeg.MuxerNull, Maps: []string{"m"}, NoVideo: true},
	})
	if err != nil {
		return in, err
	}
	in.Loudness, err = ParseEBUR128(log)
	return in, err
}
