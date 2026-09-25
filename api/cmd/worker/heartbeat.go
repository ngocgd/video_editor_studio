package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/residency"
	"loomtale/api/internal/providers/workerstatus"
)

// heartbeatInterval matches workerstatus.StaleAfter's assumption (15s
// staleness tolerates a couple of missed 5s beats).
const heartbeatInterval = 5 * time.Second

// startWorkerStatusHeartbeat upserts this process's status every
// heartbeatInterval and wires manager.OnChange (when WORKER_GPU is true)
// so a residency change is reflected immediately rather than waiting up
// to heartbeatInterval. workerID identifies this row; the hostname is
// unique per container by default, which is all a single-worker
// deployment needs.
func startWorkerStatusHeartbeat(ctx context.Context, queries *dbgen.Queries, probe pipeline.GpuProbe, manager *residency.Manager) {
	workerID, err := os.Hostname()
	if err != nil || workerID == "" {
		workerID = "worker"
	}
	store := &workerstatus.Store{Queries: queries}

	publish := func() {
		status := workerstatus.GPU{}
		if probe != nil {
			if snap, err := probe.Snapshot(ctx); err == nil {
				status = workerstatus.GPU{
					Present: snap.TotalMB > 0, TotalMB: snap.TotalMB, FreeMB: snap.FreeMB,
					BudgetMB: snap.BudgetMB, RenderReserveMB: snap.RenderReserveMB, MeasuredAt: snap.MeasuredAt,
				}
			}
		}
		residentRef := ""
		providers := map[string]workerstatus.ProviderInfo{}
		if manager != nil {
			if current := manager.Current(); current != nil {
				residentRef = current.Backend + ":" + current.Model
			}
			for name := range manager.Backends {
				providers[name] = workerstatus.ProviderInfo{Available: true}
			}
		}
		if err := store.Upsert(ctx, workerID, status, residentRef, providers); err != nil {
			slog.WarnContext(ctx, "worker_status heartbeat upsert failed", "error", err)
		}
	}

	if manager != nil {
		manager.OnChange = publish
	}

	go func() {
		publish() // report immediately at boot, not just on the first tick
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
}
