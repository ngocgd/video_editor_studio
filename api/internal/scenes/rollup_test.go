package scenes

import (
	"testing"

	"github.com/google/uuid"
)

func testInputs() (EpisodeInputs, SceneInputs) {
	style := Style{ID: uuid.New(), StylePrompt: "ink wash", BaseModel: "z-image-turbo", Steps: 8, Width: 1920, Height: 1080}
	in := EpisodeInputs{
		Lang: "en", DefaultStyle: &style, Styles: map[uuid.UUID]Style{style.ID: style},
		Characters: map[uuid.UUID]CharacterLook{linMo: {ID: linMo, AppearancePrompt: "young man", TriggerToken: "lnmo"}},
		Narrator:   &Voice{Engine: "chatterbox", Params: map[string]string{"exaggeration": "0.4"}},
		Voices:     map[uuid.UUID]Voice{linMo: {Engine: "chatterbox"}},
		GapMs:      150,
	}
	take := uuid.New()
	sc := SceneInputs{
		ID: uuid.New(), Narration: "Lin Mo did not kneel.", ImagePrompt: "a cliff in a storm", CharacterIDs: []uuid.UUID{linMo},
		Segments: []Segment{{Text: "Lin Mo did not kneel."}, {SpeakerCharacterID: &linMo, Text: `"Never."`}}, VoiceTakeID: &take,
	}
	return in, sc
}

func TestInputHashesAreStable(t *testing.T) {
	in, sc := testInputs()
	for _, kind := range []string{TakeImage, TakeVoice, TakeAlign} {
		a := ComponentsFor(kind, in, sc).Hash()
		// Map iteration order and fresh copies must not change the hash.
		in2, sc2 := testInputs()
		sc2.ID, sc2.VoiceTakeID, in2.Styles = sc.ID, sc.VoiceTakeID, in.Styles
		in2.DefaultStyle = in.DefaultStyle
		if b := ComponentsFor(kind, in2, sc2).Hash(); a != b {
			t.Fatalf("%s hash is not stable: %s vs %s", kind, a, b)
		}
		if len(a) != 64 {
			t.Fatalf("%s hash %q", kind, a)
		}
	}
}

func TestNarrationEditStalesOnlyVoiceAndAlign(t *testing.T) {
	in, sc := testInputs()
	before := map[string]Components{}
	for _, k := range []string{TakeImage, TakeVoice, TakeAlign} {
		before[k] = ComponentsFor(k, in, sc)
	}
	sc.Narration = "Lin Mo would not kneel."
	sc.Segments[0].Text = "Lin Mo would not kneel."
	if ImageComponents(in, sc).Hash() != before[TakeImage].Hash() {
		t.Fatal("a narration edit must not make the image stale")
	}
	for _, k := range []string{TakeVoice, TakeAlign} {
		now := ComponentsFor(k, in, sc)
		if now.Hash() == before[k].Hash() {
			t.Fatalf("%s must be stale after a narration edit", k)
		}
		if r := StaleReason(k, before[k], now); r != staleReasons["text"] {
			t.Fatalf("%s reason = %q", k, r)
		}
	}
}

func TestStaleReasonsNameTheChangedInput(t *testing.T) {
	in, sc := testInputs()
	img := ImageComponents(in, sc)
	sc.ImagePrompt = "a cliff at dawn"
	if r := StaleReason(TakeImage, img, ImageComponents(in, sc)); r != staleReasons["prompt"] {
		t.Fatalf("prompt reason = %q", r)
	}
	voice := VoiceComponents(in, sc)
	in.Narrator.Params = map[string]string{"exaggeration": "0.9"}
	if r := StaleReason(TakeVoice, voice, VoiceComponents(in, sc)); r != staleReasons["voices"] {
		t.Fatalf("voice reason = %q", r)
	}
	look := in.Characters[linMo]
	img = ImageComponents(in, sc)
	look.LoraVersion = 3
	in.Characters[linMo] = look
	if r := StaleReason(TakeImage, img, ImageComponents(in, sc)); r != staleReasons["characters"] {
		t.Fatalf("lora reason = %q", r)
	}
	align := AlignComponents(in, sc)
	other := uuid.New()
	sc.VoiceTakeID = &other
	if r := StaleReason(TakeAlign, align, AlignComponents(in, sc)); r != staleReasons["voiceTake"] {
		t.Fatalf("align reason = %q", r)
	}
}

func TestPipForPrecedence(t *testing.T) {
	in, sc := testInputs()
	current := ImageComponents(in, sc)
	fresh := &TakeInfo{InputHash: current.Hash(), Params: TakeParams{Components: current}}
	oldComponents := Components{"prompt": "x", "style": current["style"], "model": current["model"], "characters": current["characters"]}
	stale := &TakeInfo{InputHash: oldComponents.Hash(), Params: TakeParams{Components: oldComponents}}
	cases := []struct {
		name string
		step *StepInfo
		take *TakeInfo
		want string
	}{
		{"nothing yet", nil, nil, StateNone},
		{"done and current", &StepInfo{Status: "done"}, fresh, StateDone},
		{"done but inputs moved", &StepInfo{Status: "done"}, stale, StateStale},
		{"queued beats a stale take", &StepInfo{Status: "queued"}, stale, StateQueued},
		{"pending counts as queued", &StepInfo{Status: "pending"}, nil, StateQueued},
		{"running", &StepInfo{Status: "running"}, fresh, StateRunning},
		{"failed latest step", &StepInfo{Status: "failed", ErrorCode: "engine_not_installed"}, fresh, StateFailed},
		{"canceled with no take", &StepInfo{Status: "canceled"}, nil, StateNone},
		{"take without a step (selected by hand)", nil, fresh, StateDone},
	}
	for _, c := range cases {
		p := PipFor(TakeImage, c.step, c.take, current)
		if p.State != c.want {
			t.Fatalf("%s: state = %s, want %s", c.name, p.State, c.want)
		}
		if (p.State == StateStale) != (p.StaleReason != "") {
			t.Fatalf("%s: stale reason %q", c.name, p.StaleReason)
		}
	}
}

func TestWorstStateRollup(t *testing.T) {
	cases := map[string][]string{
		StateFailed:  {StateDone, StateStale, StateFailed, StateRunning},
		StateStale:   {StateDone, StateStale, StateRunning, StateNone},
		StateRunning: {StateDone, StateQueued, StateRunning},
		StateQueued:  {StateNone, StateQueued, StateDone},
		StateNone:    {StateDone, StateNone},
		StateDone:    {StateDone, StateDone},
	}
	for want, states := range cases {
		if got := WorstState(states...); got != want {
			t.Fatalf("WorstState(%v) = %s, want %s", states, got, want)
		}
	}
}

func TestFilterMatching(t *testing.T) {
	states := []string{StateDone, StateStale, StateNone}
	if !Matches(FilterStale, states) || !Matches(FilterMissing, states) || Matches(FilterFailed, states) || Matches(FilterInQueue, states) {
		t.Fatal("filter matching is wrong")
	}
	if !Matches(FilterInQueue, []string{StateRunning}) || !Matches(FilterAll, nil) {
		t.Fatal("in-queue / all matching is wrong")
	}
	if !NeedsGeneration(StateFailed) || NeedsGeneration(StateQueued) || NeedsGeneration(StateDone) {
		t.Fatal("generation need is wrong")
	}
}

func TestPlanMissingChainsAlignAfterVoice(t *testing.T) {
	scene := func(img, voice, align string, voiceTake bool) RolledScene {
		rs := RolledScene{Pips: map[string]Pip{TakeImage: {State: img}, TakeVoice: {State: voice}, TakeAlign: {State: align}}, Takes: map[string]*TakeInfo{}}
		if voiceTake {
			rs.Takes[TakeVoice] = &TakeInfo{}
		}
		return rs
	}
	roll := Rollup{Scenes: []RolledScene{
		scene(StateNone, StateNone, StateNone, false),     // everything
		scene(StateDone, StateStale, StateStale, true),    // voice + align
		scene(StateDone, StateDone, StateNone, true),      // align only
		scene(StateRunning, StateQueued, StateNone, false), // already queued: nothing
		scene(StateFailed, StateDone, StateDone, true),    // retry the image
	}}
	steps, counts := PlanMissing(roll, nil)
	if counts != (MissingCounts{Image: 2, Voice: 2, Align: 3}) {
		t.Fatalf("counts = %+v", counts)
	}
	voiceSteps := map[uuid.UUID]bool{}
	for _, s := range steps {
		if s.Kind == KindVoice {
			voiceSteps[s.ID] = true
		}
		if s.Priority != 3 {
			t.Fatalf("batch steps must use batch priority, got %d", s.Priority)
		}
	}
	chained := 0
	for _, s := range steps {
		if s.Kind == KindAlign && len(s.DependsOn) == 1 && voiceSteps[s.DependsOn[0]] {
			chained++
		}
	}
	if chained != 2 {
		t.Fatalf("align steps chained after voice = %d", chained)
	}
	if MissingCount(roll) != 7 {
		t.Fatalf("missing count = %d", MissingCount(roll))
	}
	if _, c := PlanMissing(roll, map[string]bool{TakeImage: true}); c != (MissingCounts{Image: 2}) {
		t.Fatalf("image-only plan = %+v", c)
	}
}

func TestScenePromptOrder(t *testing.T) {
	st := &Style{StylePrompt: "ink wash"}
	got := ScenePrompt(st, []CharacterLook{{TriggerToken: "lnmo", AppearancePrompt: "young man"}}, "a cliff")
	if got != "ink wash, lnmo, young man, a cliff" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestPlanVoiceNeedsOneEngineAndEveryVoice(t *testing.T) {
	in, sc := testInputs()
	plans, engine, err := PlanVoice(in, sc.Segments, nil)
	if err != nil || engine != "chatterbox" || len(plans) != 2 {
		t.Fatalf("plan = %v %q %v", plans, engine, err)
	}
	if plans[0].Voice.MergedParams()["exaggeration"] != "0.4" {
		t.Fatal("narrator params lost")
	}
	delete(in.Voices, linMo)
	if _, _, err := PlanVoice(in, sc.Segments, map[uuid.UUID]string{linMo: "Lin Mo"}); err == nil {
		t.Fatal("a speaker without a voice must be refused")
	}
	in.Voices[linMo] = Voice{Engine: "vieneu-v3-turbo"}
	if _, _, err := PlanVoice(in, sc.Segments, nil); err == nil {
		t.Fatal("two engines in one scene must be refused")
	}
}

func TestWavRoundTrip(t *testing.T) {
	f := WavFormat{Channels: 1, SampleRate: 24000, BitsPerSample: 16}
	b := SilenceWav(f, 150)
	got, err := ParseWav(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 24000 || got.Channels != 1 || got.DurationMs() != 150 {
		t.Fatalf("parsed %+v (%d ms)", got, got.DurationMs())
	}
	if _, err := ParseWav([]byte("not a wav")); err == nil {
		t.Fatal("garbage parsed as wav")
	}
}
