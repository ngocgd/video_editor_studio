package pipeline

import (
	"context"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
)

// Chain combines several AdmissionCheck functions into one, running them
// in order and stopping at the first error. Engine.Checks can also just
// be a plain slice; Chain exists for callers that want a single
// AdmissionCheck value (e.g. to pass around before an Engine exists).
func Chain(checks ...AdmissionCheck) AdmissionCheck {
	return func(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, stepCount int) error {
		for _, c := range checks {
			if err := c(ctx, q, tenantID, stepCount); err != nil {
				return err
			}
		}
		return nil
	}
}

// AllowAll is an AdmissionCheck that never rejects; used in tests and as
// an explicit "no admission checks" default.
func AllowAll(context.Context, *dbgen.Queries, uuid.UUID, int) error { return nil }

// EnqueueKinds describes the enqueue an AdmissionCheck is judging: the
// run kind and the kind of every step it would create. Engine.Enqueue
// puts it on the context it hands to each check, so a check can admit or
// refuse by kind (the disk watermark only guards render and model-pull
// work) without the AdmissionCheck signature changing.
type EnqueueKinds struct {
	RunKind   string
	StepKinds []string
}

type enqueueKindsKey struct{}

// WithEnqueueKinds returns ctx carrying k.
func WithEnqueueKinds(ctx context.Context, k EnqueueKinds) context.Context {
	return context.WithValue(ctx, enqueueKindsKey{}, k)
}

// EnqueueKindsFrom returns the kinds Engine.Enqueue attached to ctx; ok is
// false outside an admission check.
func EnqueueKindsFrom(ctx context.Context) (EnqueueKinds, bool) {
	k, ok := ctx.Value(enqueueKindsKey{}).(EnqueueKinds)
	return k, ok
}

func enqueueKindsOf(spec RunSpec) EnqueueKinds {
	kinds := make([]string, len(spec.Steps))
	for i, s := range spec.Steps {
		kinds[i] = s.Kind
	}
	return EnqueueKinds{RunKind: spec.Kind, StepKinds: kinds}
}
