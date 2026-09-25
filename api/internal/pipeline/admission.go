package pipeline

import (
	"context"

	"github.com/google/uuid"
)

// Chain combines several AdmissionCheck functions into one, running them
// in order and stopping at the first error. Engine.Checks can also just
// be a plain slice; Chain exists for callers that want a single
// AdmissionCheck value (e.g. to pass around before an Engine exists).
func Chain(checks ...AdmissionCheck) AdmissionCheck {
	return func(ctx context.Context, tenantID uuid.UUID, stepCount int) error {
		for _, c := range checks {
			if err := c(ctx, tenantID, stepCount); err != nil {
				return err
			}
		}
		return nil
	}
}

// AllowAll is an AdmissionCheck that never rejects; used in tests and as
// an explicit "no admission checks" default.
func AllowAll(context.Context, uuid.UUID, int) error { return nil }
