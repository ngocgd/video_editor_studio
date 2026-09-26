package library

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"loomtale/api/internal/pipeline"
)

func TestPreviewTokenIgnoresOrderButNotContent(t *testing.T) {
	s := Settings{SegmentTTLDays: 14, TakeTTLDays: 30}
	a := []Candidate{{ID: "h1"}, {ID: "h2"}}
	b := []Candidate{{ID: "h2"}, {ID: "h1"}}
	takes := []Candidate{{ID: "t1"}}
	base := previewToken(s, a, takes)
	if previewToken(s, b, takes) != base {
		t.Fatal("token depends on list order")
	}
	if previewToken(s, a, nil) == base {
		t.Fatal("token ignores a removed take")
	}
	if previewToken(s, takes, a) == previewToken(s, a, takes) {
		t.Fatal("token confuses segments with takes")
	}
	if previewToken(Settings{SegmentTTLDays: 7, TakeTTLDays: 30}, a, takes) == base {
		t.Fatal("token ignores the retention setting")
	}
}

func TestObjectKeysIncludeVariantsAndPeaks(t *testing.T) {
	variants := []byte(`{"webp":{"320":"t/x/image/a-320.webp"},"avif":{"1280":"t/x/image/a-1280.avif"},"peaks":"t/x/audio/a.peaks"}`)
	got := objectKeys("t/x/image/a", variants)
	slices.Sort(got)
	want := []string{"t/x/audio/a.peaks", "t/x/image/a", "t/x/image/a-1280.avif", "t/x/image/a-320.webp"}
	if !slices.Equal(got, want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
	if got := objectKeys("t/x/video/b", nil); len(got) != 1 {
		t.Fatalf("no variants: %v", got)
	}
}

func TestCleanupStepsHashTheirInput(t *testing.T) {
	hs := Handlers(Deps{})
	if len(hs) != 2 || hs[0].Kind() != KindCleanup || hs[1].Kind() != KindTTLCleanup {
		t.Fatalf("handlers %v", hs)
	}
	raw := func(token string) json.RawMessage {
		b, _ := jsonInput(CleanupInput{Token: token})
		return b
	}
	h1, _ := hs[1].InputHash(context.Background(), pipeline.StepRef{Input: raw("a")})
	h2, _ := hs[1].InputHash(context.Background(), pipeline.StepRef{Input: raw("b")})
	if h1 == "" || h1 == h2 {
		t.Fatalf("two runs share an input hash: %q %q", h1, h2)
	}
	if q, _ := hs[0].Queue(context.Background(), pipeline.StepRef{}); q != pipeline.QueueCPU {
		t.Fatalf("queue %q", q)
	}
}
