package analytics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
)

func i64(v int64) *int64      { return &v }
func fl64(v float64) *float64 { return &v }

func TestSumDaysKeepsMissingMetricsNil(t *testing.T) {
	got := sumDays([]ChannelDay{
		{Views: i64(10), WatchHours: fl64(1.5)},
		{Views: i64(5), SubscribersGained: i64(2)},
		{},
	})
	if *got.Views != 15 || *got.WatchHours != 1.5 || *got.SubscribersGained != 2 {
		t.Fatalf("sums = %+v", got)
	}
	if got.SubscribersLost != nil {
		t.Fatalf("subscribersLost = %v, want nil when no day had it", *got.SubscribersLost)
	}
	if empty := sumDays(nil); empty.Views != nil || empty.WatchHours != nil {
		t.Fatalf("empty series = %+v, want all nil", empty)
	}
}

func TestExplainInputCarriesNoFreeText(t *testing.T) {
	// The JSON the step sends has only numbers, dates, ids and rule
	// names: no field can hold a title or any other channel text.
	in := ExplainInput{
		Videos:   []ExplainVideo{{VideoID: "dQw4w9WgXcQ", Views: i64(3)}},
		Findings: []ExplainFinding{{Rule: RuleLowCTR, Evidence: map[string]any{"ctr": 0.01}}},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"title", "description", "tags"} {
		if strings.Contains(strings.ToLower(string(raw)), banned) {
			t.Fatalf("explain input has a %q field: %s", banned, raw)
		}
	}
}

func TestExplainOutputRoundTrip(t *testing.T) {
	out := ExplainOutput(llm.Response{Text: "Views rose.", Model: "m1", CostUSD: 0.0021}, "anthropic-api")
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeExplainOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Views rose." || got.Provider != "anthropic-api" || got.Model != "m1" || got.CostUSD != 0.0021 {
		t.Fatalf("decoded = %+v", got)
	}
}

type fakeResolver struct{ name string }

func (f fakeResolver) Resolve(context.Context, uuid.UUID, registry.Action) (llm.Provider, string, error) {
	return nil, f.name, nil
}

func (f fakeResolver) QueueFor(context.Context, uuid.UUID, registry.Action) (string, error) {
	if f.name == registry.ProviderOllama {
		return pipeline.QueueGPU, nil
	}
	return pipeline.QueueLLM, nil
}

func (f fakeResolver) ModelRefFor(name string) *pipeline.ModelRef {
	if name == registry.ProviderOllama {
		return &pipeline.ModelRef{Backend: name, Model: "qwen"}
	}
	return nil
}

func TestExplainHandlerRoutesByProvider(t *testing.T) {
	ctx := context.Background()
	ref := pipeline.StepRef{ID: uuid.New(), TenantID: uuid.New(), Kind: KindExplain}

	h := &ExplainHandler{Registry: fakeResolver{name: registry.ProviderOllama}}
	if q, _ := h.Queue(ctx, ref); q != pipeline.QueueGPU {
		t.Fatalf("ollama queue = %q", q)
	}
	if m, _ := h.ModelRef(ctx, ref); m == nil {
		t.Fatal("ollama needs a model ref")
	}
	h = &ExplainHandler{Registry: fakeResolver{name: "anthropic-api"}}
	if q, _ := h.Queue(ctx, ref); q != pipeline.QueueLLM {
		t.Fatalf("api provider queue = %q", q)
	}
	if m, _ := h.ModelRef(ctx, ref); m != nil {
		t.Fatalf("api provider model ref = %+v", m)
	}
	a, _ := h.InputHash(ctx, ref)
	ref.ID = uuid.New()
	b, _ := h.InputHash(ctx, ref)
	if a == b {
		t.Fatal("input hash must be unique per step")
	}
}
