package render

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

func sceneRow(idx int, voiceMs int32) dbgen.ListManifestSceneInputsRow {
	return dbgen.ListManifestSceneInputsRow{
		SceneID: idconv.ToPg(uuid.New()), Idx: int32(idx), MotionPreset: MotionKenBurns,
		ImageAssetID: idconv.ToPg(uuid.New()), ImageSha256: idconv.ToPgText("aa"),
		VoiceAssetID: idconv.ToPg(uuid.New()), VoiceSha256: idconv.ToPgText("bb"), VoiceDurationMs: idconv.ToPgInt4(voiceMs),
		AlignAssetID: idconv.ToPg(uuid.New()), ImageParams: []byte(`{"seed": 1, "score": 0.75}`),
	}
}

func TestManifestScenesFreezesTakesAndFrames(t *testing.T) {
	scenes, reasons := ManifestScenes([]dbgen.ListManifestSceneInputsRow{sceneRow(1, 3000), sceneRow(2, 1517)}, 30, false)
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
	if scenes[0].DurationFrames != 90 || scenes[1].DurationFrames != 46 {
		t.Fatalf("frames = %d, %d", scenes[0].DurationFrames, scenes[1].DurationFrames)
	}
	if scenes[0].ImageScore == nil || *scenes[0].ImageScore != 0.75 {
		t.Fatal("the image take's score must be carried into the manifest")
	}
}

func TestManifestScenesListsEveryMissingPrerequisite(t *testing.T) {
	noImage, noVoice, parallax := sceneRow(1, 3000), sceneRow(2, 3000), sceneRow(3, 3000)
	noImage.ImageAssetID = idconv.ToPgPtr(nil)
	noVoice.VoiceDurationMs = idconv.ToPgInt4(0)
	noVoice.VoiceDurationMs.Valid = false
	noVoice.AlignAssetID = idconv.ToPgPtr(nil)
	parallax.MotionPreset = MotionParallax
	_, reasons := ManifestScenes([]dbgen.ListManifestSceneInputsRow{noImage, noVoice, parallax}, 30, false)
	joined := strings.Join(reasons, "|")
	for _, want := range []string{"Scene 1 has no selected image", "Scene 2 has no ready voice", "Scene 2 has no subtitle alignment", "depth model not installed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("reasons %q lack %q", joined, want)
		}
	}
	if _, reasons := ManifestScenes(nil, 30, false); len(reasons) != 1 {
		t.Fatal("an episode without scenes cannot render")
	}
}

func TestStepSpecsSkipCachedAndDuplicateEntries(t *testing.T) {
	m := testManifest(3)
	tl, err := m.Timeline()
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	planned := planSteps(m, tl, id.String(), "en")
	if len(planned) != len(tl.Segments)+2 {
		t.Fatalf("planned %d steps for %d segments", len(planned), len(tl.Segments))
	}
	cachedHash := planned[0].Input.Hash
	dup := append(planned, planned[1])
	specs, reused, err := stepSpecs(dup, []string{cachedHash}, id, m.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if reused != 1 || len(specs) != len(planned)-1+2 {
		t.Fatalf("reused %d, %d specs", reused, len(specs))
	}
	compose, preview := specs[len(specs)-2], specs[len(specs)-1]
	if compose.Kind != KindCompose || len(compose.DependsOn) != len(specs)-2 {
		t.Fatalf("compose must depend on every encode step: %+v", compose)
	}
	if preview.Kind != KindPreview || len(preview.DependsOn) != 1 || preview.DependsOn[0] != compose.ID {
		t.Fatal("preview depends on compose only")
	}
	var in StepInput
	if err := json.Unmarshal(compose.Input, &in); err != nil || in.Hash != m.Hash() || in.ManifestID != id.String() {
		t.Fatalf("compose input = %s", compose.Input)
	}
	// The input hash a step is stored with is the one frozen in its input.
	hash, err := stepBase{}.InputHash(context.Background(), pipeline.StepRef{Input: specs[0].Input})
	if err != nil || hash != planned[1].Input.Hash {
		t.Fatalf("input hash = %q, %v", hash, err)
	}
	if _, err := (stepBase{}).InputHash(context.Background(), pipeline.StepRef{}); !errors.Is(err, pipeline.ErrValidation) {
		t.Fatal("a render step without its frozen hash is refused")
	}
}

func TestSettingsFromRowReadsStyle(t *testing.T) {
	d := DefaultSettings()
	row := dbgen.RenderSetting{
		Width: 1280, Height: 720, Fps: 25, Encoder: EncoderX264, Subtitles: SubtitlesSRT,
		SubtitleStyle: []byte(`{"font":"Literata","sizePx":36,"position":"top","shadowPx":1}`),
		DefaultMotion: MotionStatic, CrossfadeMs: 400, LoudnessLufsX10: -160, TruePeakDbtpX10: -20,
	}
	s, err := SettingsFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if s.SubtitleStyle.SizePx != 36 || s.SubtitleStyle.Position != "top" || s.FPS != 25 || s == d {
		t.Fatalf("settings = %+v", s)
	}
	row.SubtitleStyle = []byte(`{"font":"x}{\\an8","sizePx":36,"position":"top","shadowPx":1}`)
	if _, err := SettingsFromRow(row); err == nil {
		t.Fatal("a font name that could inject ASS is refused")
	}
}

func TestNotReadyErrorIsValidation(t *testing.T) {
	var err error = &NotReadyError{Reasons: []string{"Scene 1 has no selected image."}}
	if !errors.Is(err, pipeline.ErrValidation) || !strings.Contains(err.Error(), "Scene 1") {
		t.Fatal("not-ready is a validation error carrying its reasons")
	}
}
