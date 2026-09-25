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
