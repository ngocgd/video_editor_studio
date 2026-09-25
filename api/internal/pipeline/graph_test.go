package pipeline

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidateGraphAcceptsAValidDAG(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	steps := []StepSpec{
		{ID: a},
		{ID: b, DependsOn: []uuid.UUID{a}},
		{ID: c, DependsOn: []uuid.UUID{a, b}},
	}
	if err := validateGraph(steps); err != nil {
		t.Fatalf("expected a valid DAG to pass, got %v", err)
	}
}

func TestValidateGraphRejectsSelfDependency(t *testing.T) {
	a := uuid.New()
	steps := []StepSpec{{ID: a, DependsOn: []uuid.UUID{a}}}
	if err := validateGraph(steps); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a self-dependency, got %v", err)
	}
}

func TestValidateGraphRejectsDependencyOutsideTheRequest(t *testing.T) {
	a, outside := uuid.New(), uuid.New()
	steps := []StepSpec{{ID: a, DependsOn: []uuid.UUID{outside}}}
	if err := validateGraph(steps); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a dependency outside the request, got %v", err)
	}
}

func TestValidateGraphRejectsACycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	steps := []StepSpec{
		{ID: a, DependsOn: []uuid.UUID{b}},
		{ID: b, DependsOn: []uuid.UUID{a}},
	}
	if err := validateGraph(steps); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a cycle, got %v", err)
	}
}

func TestValidateGraphRejectsALongerCycle(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	steps := []StepSpec{
		{ID: a, DependsOn: []uuid.UUID{c}},
		{ID: b, DependsOn: []uuid.UUID{a}},
		{ID: c, DependsOn: []uuid.UUID{b}},
	}
	if err := validateGraph(steps); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for a 3-step cycle, got %v", err)
	}
}

func TestValidateGraphRejectsDuplicateStepIDs(t *testing.T) {
	a := uuid.New()
	steps := []StepSpec{{ID: a}, {ID: a}}
	if err := validateGraph(steps); !errors.Is(err, ErrInvalidGraph) {
		t.Fatalf("expected ErrInvalidGraph for duplicate step ids, got %v", err)
	}
}

func TestValidateGraphAcceptsEmpty(t *testing.T) {
	if err := validateGraph(nil); err != nil {
		t.Fatalf("expected an empty graph to be valid, got %v", err)
	}
}
