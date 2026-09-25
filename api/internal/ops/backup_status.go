// Package ops holds small operational status checks surfaced through
// /readyz (e.g. backup freshness), as opposed to the hard up/down checks
// in package health.
package ops

import (
	"context"
	"fmt"
	"time"

	"loomtale/api/internal/db/gen"
)

// staleAfter matches the plan's dashboard warning threshold.
const staleAfter = 36 * time.Hour

// BackupStatus reports the freshness of the most recent backup run.
type BackupStatus struct {
	Queries *gen.Queries
}

// Status returns a short human-readable string for the /readyz "backup"
// check: "ok", "stale (last success 40h ago)", "no backup run yet", or the
// failure detail of the most recent run.
func (b *BackupStatus) Status(ctx context.Context) string {
	run, err := b.Queries.GetLatestBackupRun(ctx)
	if err != nil {
		return "no backup run yet"
	}
	if run.Status == "failed" {
		detail := "backup failed"
		if run.Detail.Valid {
			detail = "backup failed: " + run.Detail.String
		}
		return detail
	}
	if run.Status == "running" {
		return "backup in progress"
	}
	age := time.Since(run.FinishedAt.Time)
	if age > staleAfter {
		return fmt.Sprintf("stale (last success %.0fh ago)", age.Hours())
	}
	return "ok"
}
