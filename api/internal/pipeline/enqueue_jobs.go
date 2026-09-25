package pipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// readyUniqueStates is the set of River job states in which a duplicate
// insert for the same step set must be rejected: a step already
// available, scheduled, running or retryable already has a live job, so
// the reconciler's ready-sweep and River's own stuck-job rescue can both
// try to enqueue the same step without ever double-queuing it.
var readyUniqueStates = []rivertype.JobState{
	rivertype.JobStateAvailable,
	rivertype.JobStateScheduled,
	rivertype.JobStateRunning,
	rivertype.JobStateRetryable,
	rivertype.JobStatePending,
}

// enqueueReadySteps groups every "queued" step by (queue, kind), chunks
// each group to BatchChunkTarget using e.Estimator, and inserts one River
// job per chunk inside tx.
func (e *Engine) enqueueReadySteps(ctx context.Context, tx pgx.Tx, steps []dbgen.PipelineStep) error {
	type groupKey struct{ queue, kind string }
	groups := make(map[groupKey][]dbgen.PipelineStep)
	for _, s := range steps {
		if s.Status != StatusQueued {
			continue
		}
		key := groupKey{queue: s.Queue, kind: s.Kind}
		groups[key] = append(groups[key], s)
	}

	// Deterministic order keeps tests (and logs) reproducible.
	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].queue != keys[j].queue {
			return keys[i].queue < keys[j].queue
		}
		return keys[i].kind < keys[j].kind
	})

	for _, key := range keys {
		group := groups[key]
		size := ChunkSize(e.Estimator(key.kind))
		metadata, err := json.Marshal(stepJobMetadata{Kind: key.kind})
		if err != nil {
			return err
		}
		for _, chunk := range ChunkIDs(stepIDs(group), size) {
			priority := minPriority(group, chunk)
			if _, err := e.River.InsertTx(ctx, tx, StepJobArgs{StepIDs: chunk}, &river.InsertOpts{
				Queue:       key.queue,
				Priority:    priority,
				MaxAttempts: MaxTransientAttempts,
				Metadata:    metadata,
				UniqueOpts: river.UniqueOpts{
					ByArgs:  true,
					ByQueue: true,
					ByState: readyUniqueStates,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func stepIDs(steps []dbgen.PipelineStep) []uuid.UUID {
	ids := make([]uuid.UUID, len(steps))
	for i, s := range steps {
		ids[i] = idconv.FromPg(s.ID)
	}
	return ids
}

func minPriority(steps []dbgen.PipelineStep, chunk []uuid.UUID) int {
	want := make(map[uuid.UUID]bool, len(chunk))
	for _, id := range chunk {
		want[id] = true
	}
	best := PriorityTrainBench
	for _, s := range steps {
		if !want[idconv.FromPg(s.ID)] {
			continue
		}
		if int(s.Priority) < best {
			best = int(s.Priority)
		}
	}
	return best
}
