package pipelineapi

import (
	"fmt"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
)

// priorityForClass maps a client-declared run urgency class to the
// server-side numeric priority every step in the run gets: a client can
// declare intent (this is an interactive regenerate vs. a batch job) but
// can never set a raw priority integer directly, which is what stops any
// editor from marking batch work interactive and starving every other
// tenant on the single GPU.
func priorityForClass(class gen.CreateRunRequestPriorityClass) (int, error) {
	switch class {
	case "interactive":
		return pipeline.PriorityInteractive, nil
	case "scene":
		return pipeline.PriorityScene, nil
	case "batch":
		return pipeline.PriorityBatch, nil
	case "train_bench":
		return pipeline.PriorityTrainBench, nil
	default:
		return 0, fmt.Errorf("%w: unknown priorityClass %q", pipeline.ErrInvalidGraph, class)
	}
}

// resolveStepGraph turns the wire request's clientRef-linked steps into
// real, server-generated pipeline.StepSpecs: every clientRef becomes a
// fresh uuid.NewV7 id, and every dependsOn entry is resolved from
// clientRef to that real id. A dependsOn entry that does not name
// another clientRef in this same request is rejected here, before
// Enqueue's own (redundant, defence-in-depth) graph validation ever
// runs.
func resolveStepGraph(specs []gen.CreateStepSpec, priority int) ([]pipeline.StepSpec, error) {
	ids := make(map[string]uuid.UUID, len(specs))
	for _, s := range specs {
		if _, dup := ids[s.ClientRef]; dup {
			return nil, fmt.Errorf("%w: duplicate clientRef %q", pipeline.ErrInvalidGraph, s.ClientRef)
		}
		ids[s.ClientRef] = idconv.NewV7()
	}

	out := make([]pipeline.StepSpec, len(specs))
	for i, s := range specs {
		var dependsOn []uuid.UUID
		if s.DependsOn != nil {
			dependsOn = make([]uuid.UUID, 0, len(*s.DependsOn))
			for _, ref := range *s.DependsOn {
				depID, ok := ids[ref]
				if !ok {
					return nil, fmt.Errorf("%w: step %q depends on clientRef %q, which is not in this request", pipeline.ErrInvalidGraph, s.ClientRef, ref)
				}
				dependsOn = append(dependsOn, depID)
			}
		}
		out[i] = pipeline.StepSpec{
			ID:        ids[s.ClientRef],
			Kind:      s.Kind,
			ScopeKind: s.ScopeKind,
			ScopeID:   s.ScopeId,
			Priority:  priority,
			DependsOn: dependsOn,
		}
	}
	return out, nil
}
