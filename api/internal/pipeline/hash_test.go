package pipeline

import "testing"

func TestHashInputsIsOrderIndependentForMapKeys(t *testing.T) {
	a, err := HashInputs(map[string]any{"b": 1, "a": 2})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashInputs(map[string]any{"a": 2, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected identical hashes for the same map regardless of construction order, got %q and %q", a, b)
	}
}

func TestHashInputsChangesWithContent(t *testing.T) {
	a, err := HashInputs("prompt", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashInputs("prompt", 2)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected different inputs to hash differently")
	}
}

func TestHashInputsIsDeterministic(t *testing.T) {
	a, err := HashInputs("scene-1", map[string]any{"seed": 42, "steps": 20})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashInputs("scene-1", map[string]any{"seed": 42, "steps": 20})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("expected repeated hashing of the same inputs to be stable")
	}
}
