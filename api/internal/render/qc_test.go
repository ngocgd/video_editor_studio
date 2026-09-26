package render

import (
	"strconv"
	"strings"
	"testing"

	"loomtale/api/internal/media/ffmpeg"
)

func probeOf(videoS, audioS string, subs bool) *ffmpeg.ProbeResult {
	p := &ffmpeg.ProbeResult{Streams: []ffmpeg.ProbeStream{
		{Index: 0, CodecType: "video", Duration: videoS}, {Index: 1, CodecType: "audio", Duration: audioS},
	}}
	if subs {
		p.Streams = append(p.Streams, ffmpeg.ProbeStream{Index: 2, CodecType: "subtitle"})
	}
	return p
}

func keyframesAt(t Timeline) []float64 {
	var out []float64
	for _, s := range t.Segments {
		out = append(out, float64(s.StartFrame)/float64(t.FPS))
	}
	return out
}

func TestEvaluatePassesAConformingRender(t *testing.T) {
	m := testManifest(3)
	tl, _ := m.Timeline()
	secs := strconv.FormatFloat(float64(tl.DurationMs())/1000, 'f', 3, 64)
	r := evaluate(m, tl, qcInputs{
		Probe: probeOf(secs, secs, true), Keyframes: keyframesAt(tl), SoftSubs: true,
		Loudness: Loudness{IntegratedLUFS: -14.2, TruePeakDBTP: -1.3},
	})
	if !r.Passed || len(r.Failures) != 0 {
		t.Fatalf("report failed: %v", r.Failures)
	}
	if r.Encoder != m.Settings.Encoder || len(r.SceneScores) != 3 || r.MaxSubtitleDriftMs > 17 {
		t.Fatalf("report = %+v", r)
	}
}

func TestEvaluateFlagsEveryBrokenCheck(t *testing.T) {
	m := testManifest(3)
	m.Scenes[1].Placeholder = true
	tl, _ := m.Timeline()
	r := evaluate(m, tl, qcInputs{
		Probe: probeOf("1.000", "1.500", false), Keyframes: []float64{0}, SoftSubs: true,
		Loudness: Loudness{IntegratedLUFS: -18, TruePeakDBTP: 0.2},
	})
	joined := strings.Join(r.Failures, "|")
	for _, want := range []string{"streams", "duration", "audio and video", "integrated loudness", "true peak", "placeholders", "keyframe"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures %q lack %q", joined, want)
		}
	}
	if r.Passed {
		t.Fatal("a broken render must not pass")
	}
}

func TestPreviewWidthKeepsAspect(t *testing.T) {
	if w := previewWidth(DefaultSettings()); w != 960 {
		t.Fatalf("1080p preview width = %d", w)
	}
	s := DefaultSettings()
	s.Width, s.Height = 1080, 1920
	if w := previewWidth(s); w%2 != 0 || w != 304 {
		t.Fatalf("portrait preview width = %d", w)
	}
}
