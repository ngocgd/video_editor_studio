package pipeline

import (
	"reflect"
	"testing"

	dbgen "loomtale/api/internal/db/gen"
)

type inputFixture struct {
	Lang        string   `json:"lang"`
	Paragraphs  []string `json:"paragraphIds,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
}

func TestStepContextInputDecodesStoredJSON(t *testing.T) {
	sc := newStepContext(t.Context(), nil, nil, dbgen.PipelineStep{
		Input: []byte(`{"lang":"vi","paragraphIds":["p1","p2"],"instruction":"make it tenser"}`),
	}, nil)

	var got inputFixture
	if err := sc.Input(&got); err != nil {
		t.Fatal(err)
	}
	if got.Lang != "vi" || len(got.Paragraphs) != 2 || got.Instruction != "make it tenser" {
		t.Fatalf("decoded input = %+v", got)
	}
}

func TestStepContextInputHandlesEmptyStoredValue(t *testing.T) {
	sc := newStepContext(t.Context(), nil, nil, dbgen.PipelineStep{}, nil)

	var got inputFixture
	if err := sc.Input(&got); err != nil {
		t.Fatalf("decoding an absent input should be a no-op, got error: %v", err)
	}
	if !reflect.DeepEqual(got, inputFixture{}) {
		t.Fatalf("expected zero value, got %+v", got)
	}
}
