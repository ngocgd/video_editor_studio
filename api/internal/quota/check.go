// Package quota implements the pipeline.AdmissionCheck that enforces a
// tenant's configured concurrent-step limit. A tenant with no
// tenant_quotas row, or a NULL max_active_steps, is unlimited: the local
// default deployment has no quota row for anyone.
package quota

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// Checker enforces tenant_quotas.max_active_steps.
type Checker struct {
	Queries *dbgen.Queries
}

// Check implements pipeline.AdmissionCheck.
func (c *Checker) Check(ctx context.Context, tenantID uuid.UUID, stepCount int) error {
	quota, err := c.Queries.GetTenantQuota(ctx, idconv.ToPg(tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no row: unlimited
	}
	if err != nil {
		return fmt.Errorf("quota: load tenant quota: %w", err)
	}
	if !quota.MaxActiveSteps.Valid {
		return nil // explicit NULL: unlimited
	}

	active, err := c.Queries.CountActiveStepsForTenant(ctx, idconv.ToPg(tenantID))
	if err != nil {
		return fmt.Errorf("quota: count active steps: %w", err)
	}

	if active+int64(stepCount) > int64(quota.MaxActiveSteps.Int32) {
		return fmt.Errorf("%w: %d active + %d requested exceeds limit %d", pipeline.ErrQuotaExceeded, active, stepCount, quota.MaxActiveSteps.Int32)
	}
	return nil
}
