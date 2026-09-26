package scenes

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRiskOfCountsOnlyDroppedScenes(t *testing.T) {
	existing := []ExistingScene{
		{TextHash: TextHash("kept, edited, with takes"), Edited: true, TakeCount: 3},
		{TextHash: TextHash("edited narration"), Edited: true, TakeCount: 2},
		{TextHash: TextHash("plain, with a take"), TakeCount: 1},
		{TextHash: TextHash("plain")},
	}
	hashes := make([]string, len(existing))
	for i, e := range existing {
		hashes[i] = e.TextHash
	}
	plan := PlanResplit(hashes, []Draft{{Narration: "kept, edited, with takes"}, {Narration: "a new paragraph"}})
	got := RiskOf(existing, plan)
	if want := (DropRisk{Dropped: 3, Edited: 1, Takes: 3}); got != want {
		t.Fatalf("risk = %+v, want %+v", got, want)
	}
	if !got.LosesWork() {
		t.Fatal("dropping an edited scene must count as losing work")
	}
}

func TestRiskWithoutEditsOrTakesNeedsNoConfirmation(t *testing.T) {
	existing := []ExistingScene{{TextHash: TextHash("a")}, {TextHash: TextHash("b")}}
	if r := RiskOfAll(existing); r.LosesWork() || r.Dropped != 2 {
		t.Fatalf("plain generated scenes: %+v", r)
	}
	if r := RiskOfAll(nil); r.LosesWork() || r.Dropped != 0 {
		t.Fatalf("a first split: %+v", r)
	}
}

func TestDropsWorkErrorMatchesAndStatesTheCounts(t *testing.T) {
	err := fmt.Errorf("apply: %w", &DropsWorkError{Risk: DropRisk{Dropped: 4, Edited: 2, Takes: 5}, UpperBound: true})
	if !errors.Is(err, ErrDropsWork) {
		t.Fatal("DropsWorkError must match ErrDropsWork")
	}
	var drops *DropsWorkError
	if !errors.As(err, &drops) || drops.Risk.Edited != 2 {
		t.Fatalf("errors.As = %+v", drops)
	}
	msg := err.Error()
	for _, want := range []string{"up to 4 scenes", "2 edited scenes", "5 takes"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q lacks %q", msg, want)
		}
	}
}
