package render

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
)

func TestSettingsRoundTripThroughAPI(t *testing.T) {
	s := DefaultSettings()
	s.Encoder, s.Subtitles, s.DefaultMotion = EncoderX264, SubtitlesSRT, MotionStatic
	s.LoudnessLUFSx10, s.TruePeakDBTPx10 = -161, -15
	got := settingsFromAPI(settingsToAPI(s))
	if got != s {
		t.Fatalf("round trip %+v, want %+v", got, s)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
}

func takeRow(image, voice, align bool) dbgen.ListManifestSceneInputsRow {
	var r dbgen.ListManifestSceneInputsRow
	if image {
		r.ImageAssetID = idconv.ToPg(idconv.NewV7())
	}
	if voice {
		r.VoiceAssetID = idconv.ToPg(idconv.NewV7())
		r.VoiceDurationMs = pgtype.Int4{Int32: 3000, Valid: true}
	}
	if align {
		r.AlignAssetID = idconv.ToPg(idconv.NewV7())
	}
	return r
}

func stageByKey(stages []gen.RenderStage) map[string]gen.RenderStage {
	out := map[string]gen.RenderStage{}
	for _, s := range stages {
		out[string(s.Key)] = s
	}
	return out
}

func TestStagesSummariseTakesAndLatestManifest(t *testing.T) {
	rows := []dbgen.ListManifestSceneInputsRow{takeRow(true, true, true), takeRow(true, false, false), takeRow(false, false, false)}
	got := stageByKey(stages(rows, manifestProgress{}, false))
	if len(got) != 7 {
		t.Fatalf("got %d stages", len(got))
	}
	check := func(key string, done, total int, state string) {
		t.Helper()
		s := got[key]
		if s.Done != done || s.Total != total || string(s.State) != state {
			t.Errorf("%s: %d/%d %s, want %d/%d %s", key, s.Done, s.Total, s.State, done, total, state)
		}
	}
	check("script", 1, 1, "done")
	check("scenes", 3, 3, "done")
	check("images", 2, 3, "partial")
	check("voice", 1, 3, "partial")
	check("subtitles", 1, 3, "partial")
	check("compose", 0, 0, "idle")
	check("encode", 0, 1, "idle")

	running := stageByKey(stages(rows, manifestProgress{latest: &gen.RenderRestart{}, cached: 4, total: 9}, true))
	if running["compose"].State != "running" || running["compose"].Done != 4 || running["encode"].State != "running" {
		t.Errorf("running render: %+v %+v", running["compose"], running["encode"])
	}
	done := stageByKey(stages(rows, manifestProgress{latest: &gen.RenderRestart{}, cached: 9, total: 9, rendered: true}, false))
	if done["compose"].State != "done" || done["encode"].State != "done" {
		t.Errorf("finished render: %+v %+v", done["compose"], done["encode"])
	}
	empty := stageByKey(stages(nil, manifestProgress{}, false))
	if empty["script"].State != "missing" || empty["scenes"].State != "missing" || empty["images"].State != "missing" {
		t.Errorf("no scenes: %+v", empty)
	}
}

func TestRenderToAPIReadsTheStoredReport(t *testing.T) {
	report := QCReport{Passed: true, IntegratedLUFS: -14.1, Encoder: EncoderX264, SHA256: "abc", DurationMs: 5200}
	report.Streams.Video, report.Streams.Audio = 1, 1
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderToAPI(dbgen.Render{ID: idconv.ToPg(idconv.NewV7()), Lang: "en", DurationMs: 5200, Encoder: EncoderX264, Report: raw})
	if err != nil {
		t.Fatal(err)
	}
	r := got.Report
	if !r.Passed || r.Sha256 != "abc" || r.Streams.Video != 1 || r.DurationMs != 5200 || r.Failures == nil || r.SceneScores == nil {
		t.Fatalf("report %+v", r)
	}
	if got.SrtAssetId != nil || got.PreviewAssetId != nil {
		t.Fatalf("absent assets must stay nil: %+v", got)
	}
}
