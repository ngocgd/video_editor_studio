package library

import (
	"context"
	"log/slog"
	"time"

	"loomtale/api/internal/db/idconv"
)

// schedulerInterval is how often the scheduler looks for tenants whose
// daily cleanup is due.
const schedulerInterval = time.Hour

// RunScheduler enqueues each tenant's library.ttl_cleanup once a day
// until ctx ends. Run it in exactly one process (the api): the due check
// and the mark are not locked against a second scheduler.
func (s *Service) RunScheduler(ctx context.Context) {
	s.scheduleDue(ctx)
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleDue(ctx)
		}
	}
}

// scheduleDue enqueues a cleanup for every tenant that is due and marks
// it, so a failed cleanup is retried the next day rather than hourly.
func (s *Service) scheduleDue(ctx context.Context) {
	ids, err := s.Queries.ListTenantsForCleanup(ctx)
	if err != nil {
		slog.WarnContext(ctx, "library: list tenants due for cleanup", "error", err)
		return
	}
	for _, id := range ids {
		tenantID := idconv.FromPg(id)
		if _, err := s.enqueue(ctx, tenantID, KindTTLCleanup, CleanupInput{}, nil); err != nil {
			slog.WarnContext(ctx, "library: enqueue daily cleanup", "error", err, "tenant_id", tenantID)
			continue
		}
		if err := s.Queries.MarkLibraryCleanup(ctx, id); err != nil {
			slog.WarnContext(ctx, "library: mark daily cleanup", "error", err, "tenant_id", tenantID)
		}
	}
}
