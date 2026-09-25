package pipelineapi

import (
	"errors"
	"testing"

	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
)

func TestPriorityForClassMapsEveryDeclaredClass(t *testing.T) {
	cases := map[gen.CreateRunRequestPriorityClass]int{
		"interactive": pipeline.PriorityInteractive,
		"scene":       pipeline.PriorityScene,
		"batch":       pipeline.PriorityBatch,
		"train_bench": pipeline.PriorityTrainBench,
	}
	for class, want := range cases {
		got, err := priorityForClass(class)
		if err != nil {
			t.Fatalf("priorityForClass(%q): %v", class, err)
		}
		if got != want {
			t.Fatalf("priorityForClass(%q) = %d, want %d", class, got, want)
		}
	}
}

func TestPriorityForClassRejectsUnknown(t *testing.T) {
	if _, err := priorityForClass("urgent"); !errors.Is(err, pipeline.ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for an unknown priority class, got %v", err)
	}
}

func TestResolveStepGraphWiresClientRefsToServerGeneratedIDs(t *testing.T) {
	specs := []gen.CreateStepSpec{
		{ClientRef: "a", Kind: "kind-a", ScopeKind: "test"},
		{ClientRef: "b", Kind: "kind-b", ScopeKind: "test", DependsOn: &[]string{"a"}},
	}
	steps, err := resolveStepGraph(specs, pipeline.PriorityBatch)
	if err != nil {
		t.Fatalf("resolveStepGraph: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	// Every id is server-generated (non-zero, distinct), never client-chosen.
	if steps[0].ID == steps[1].ID {
		t.Fatal("expected distinct server-generated ids")
	}
	if len(steps[1].DependsOn) != 1 || steps[1].DependsOn[0] != steps[0].ID {
		t.Fatalf("expected step b's dependsOn to resolve to step a's generated id, got %+v", steps[1].DependsOn)
	}
	for _, s := range steps {
		if s.Priority != pipeline.PriorityBatch {
			t.Fatalf("expected every step to get the run's derived priority, got %d", s.Priority)
		}
	}
}

func TestResolveStepGraphRejectsUnknownDependsOnRef(t *testing.T) {
	specs := []gen.CreateStepSpec{
		{ClientRef: "a", Kind: "kind-a", ScopeKind: "test", DependsOn: &[]string{"nonexistent"}},
	}
	if _, err := resolveStepGraph(specs, pipeline.PriorityBatch); !errors.Is(err, pipeline.ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a dependsOn referencing an unknown clientRef, got %v", err)
	}
}

func TestResolveStepGraphRejectsDuplicateClientRef(t *testing.T) {
	specs := []gen.CreateStepSpec{
		{ClientRef: "dup", Kind: "kind-a", ScopeKind: "test"},
		{ClientRef: "dup", Kind: "kind-b", ScopeKind: "test"},
	}
	if _, err := resolveStepGraph(specs, pipeline.PriorityBatch); !errors.Is(err, pipeline.ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a duplicate clientRef, got %v", err)
	}
}
