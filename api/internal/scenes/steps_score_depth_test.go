package scenes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

func ref(char uuid.UUID, approved bool) dbgen.CharacterRef {
	return dbgen.CharacterRef{ID: idconv.ToPg(uuid.New()), CharacterID: idconv.ToPg(char), AssetID: idconv.ToPg(uuid.New()), Approved: approved}
}

func TestPickScoreRefsTakesApprovedRefsInTurnPerCharacter(t *testing.T) {
	a, b, other := uuid.New(), uuid.New(), uuid.New()
	a1, a2, a3 := ref(a, true), ref(a, true), ref(a, true)
	b1 := ref(b, true)
	refs := []dbgen.CharacterRef{a1, ref(a, false), a2, ref(other, true), b1, a3, ref(b, false)}

	got := PickScoreRefs([]uuid.UUID{a, b}, refs, 3)
	want := []dbgen.CharacterRef{a1, b1, a2}
	if len(got) != len(want) {
		t.Fatalf("got %d refs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("ref %d: got %v, want %v", i, got[i].ID, want[i].ID)
		}
	}
	if all := PickScoreRefs([]uuid.UUID{a, b}, refs, MaxScoreRefs); len(all) != 4 {
		t.Fatalf("without a tight limit: got %d refs, want the 4 approved refs of a and b", len(all))
	}
}

func TestPickScoreRefsWithoutApprovedRefsIsEmpty(t *testing.T) {
	a := uuid.New()
	if got := PickScoreRefs([]uuid.UUID{a}, []dbgen.CharacterRef{ref(a, false)}, MaxScoreRefs); len(got) != 0 {
		t.Fatalf("got %d refs, want none", len(got))
	}
	if got := PickScoreRefs(nil, []dbgen.CharacterRef{ref(a, true)}, MaxScoreRefs); len(got) != 0 {
		t.Fatalf("a scene without characters got %d refs, want none", len(got))
	}
}

func TestAnalysisStepsOnlyForInstalledEngines(t *testing.T) {
	ctx := context.Background()
	scene, take := uuid.New(), uuid.New()
	notInstalled := errors.New("not installed")
	installed := func(names ...string) func(context.Context, string) error {
		return func(_ context.Context, name string) error {
			for _, n := range names {
				if n == name {
					return nil
				}
			}
			return notInstalled
		}
	}
	kinds := func(steps []pipeline.StepSpec) []string {
		var out []string
		for _, s := range steps {
			out = append(out, s.Kind)
		}
		return out
	}

	if got := AnalysisSteps(ctx, scene, take, true, nil); len(got) != 0 {
		t.Fatalf("no installed check: got %v, want no steps", kinds(got))
	}
	if got := AnalysisSteps(ctx, scene, take, true, installed()); len(got) != 0 {
		t.Fatalf("nothing installed: got %v, want no steps", kinds(got))
	}
	if got := kinds(AnalysisSteps(ctx, scene, take, false, installed(ScoreEngine, DepthEngine))); len(got) != 1 || got[0] != KindDepth {
		t.Fatalf("no characters: got %v, want only %s", got, KindDepth)
	}
	steps := AnalysisSteps(ctx, scene, take, true, installed(ScoreEngine, DepthEngine))
	if got := kinds(steps); len(got) != 2 || got[0] != KindScore || got[1] != KindDepth {
		t.Fatalf("both installed: got %v, want [%s %s]", got, KindScore, KindDepth)
	}
	for _, s := range steps {
		var in AnalysisInput
		if err := json.Unmarshal(s.Input, &in); err != nil || in.TakeID != take {
			t.Fatalf("%s input %s does not name take %s", s.Kind, s.Input, take)
		}
		if s.ScopeKind != ScopeScene || s.ScopeID != scene {
			t.Fatalf("%s scoped to %s %s, want scene %s", s.Kind, s.ScopeKind, s.ScopeID, scene)
		}
	}
}

func TestAnalysisHashDependsOnKindAndInputs(t *testing.T) {
	take, r1, r2 := uuid.New(), uuid.New(), uuid.New()
	h := analysisHash(KindScore, take, r1)
	if h != analysisHash(KindScore, take, r1) {
		t.Fatal("the hash is not stable")
	}
	for _, other := range []string{analysisHash(KindDepth, take, r1), analysisHash(KindScore, take, r1, r2), analysisHash(KindScore, take)} {
		if other == h {
			t.Fatal("a different kind or input set hashed the same")
		}
	}
}

func TestHandlersIncludeTheAnalysisSteps(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range Handlers(StepDeps{}) {
		seen[h.Kind()] = true
	}
	for _, k := range []string{KindScore, KindDepth} {
		if !seen[k] {
			t.Fatalf("Handlers is missing %s", k)
		}
	}
	for _, h := range []pipeline.StepHandler{&ScoreHandler{}, &DepthHandler{}} {
		m, err := h.ModelRef(context.Background(), pipeline.StepRef{})
		if err != nil || m == nil || m.Backend != pyworkerBackend {
			t.Fatalf("%s: model ref %+v, %v; want a pyworker model", h.Kind(), m, err)
		}
		if q, _ := h.Queue(context.Background(), pipeline.StepRef{}); q != pipeline.QueueGPU {
			t.Fatalf("%s queue %q, want %q", h.Kind(), q, pipeline.QueueGPU)
		}
	}
}
