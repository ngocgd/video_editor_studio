package pipeline

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrInvalidGraph is returned by Enqueue when a run's step graph is
// malformed: a dependency outside the request, a self-dependency, or a
// cycle. Callers map it to a 400.
var ErrInvalidGraph = errors.New("pipeline: invalid step graph")

// ErrUnknownStepKind is returned by Enqueue when a step names a kind no
// handler is registered for. Callers map it to a 400: this is the
// caller's mistake (a typo, a kind that belongs to a phase not deployed
// yet), never a server fault.
var ErrUnknownStepKind = errors.New("pipeline: no handler registered for step kind")

// validateGraph checks that every DependsOn entry in steps refers to
// another step in the same slice (never itself, never a step id that
// exists only outside this request — Enqueue always creates a brand new
// run, so nothing outside this request could be a legitimate dependency
// yet) and that the resulting dependency graph is acyclic.
func validateGraph(steps []StepSpec) error {
	byID := make(map[uuid.UUID]StepSpec, len(steps))
	for _, s := range steps {
		if _, dup := byID[s.ID]; dup {
			return fmt.Errorf("%w: duplicate step id %s", ErrInvalidGraph, s.ID)
		}
		byID[s.ID] = s
	}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return fmt.Errorf("%w: step %s depends on itself", ErrInvalidGraph, s.ID)
			}
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("%w: step %s depends on %s, which is not in this request", ErrInvalidGraph, s.ID, dep)
			}
		}
	}

	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[uuid.UUID]int, len(steps))
	var visit func(id uuid.UUID) error
	visit = func(id uuid.UUID) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("%w: dependency cycle through step %s", ErrInvalidGraph, id)
		}
		state[id] = visiting
		for _, dep := range byID[id].DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	for _, s := range steps {
		if err := visit(s.ID); err != nil {
			return err
		}
	}
	return nil
}
