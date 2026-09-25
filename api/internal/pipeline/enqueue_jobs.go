package pipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// enqueueReadySteps groups every "queued" step by (queue, kind,
// provider_ref) — provider_ref so a GPU chunk never mixes two different
// models, letting the executor load exactly one model for the whole
// chunk — chunks each group to BatchChunkTarget using e.Estimator, and
// inserts one River job per chunk inside tx.
//
// Deliberately uninque: no river.UniqueOpts. The DB-side CAS claim is the
// only fence a handler ever trusts, and every caller of this function
// only ever passes rows it just, in the same transaction, moved into
// "queued" via a WHERE clause that itself cannot double-fire (see
// MarkStepsQueued, ResetStaleHeartbeatsBatch, ReadySweepBatch, RetryStep)
// — so a duplicate River job for the same step set is not just harmless,
// it cannot actually happen from this codebase's own call sites either.
// A prior version used UniqueOpts keyed by args, which seemed like
// harmless extra safety but actively broke crash recovery: after a
// worker died, River leaves its job row "running" until
// RescueStuckJobsAfter (4h); the reconciler's re-enqueue for the same
// step ids was then silently deduped against that dead, still-"running"
// row, stalling the step for the entire rescue window.
func (e *Engine) enqueueReadySteps(ctx context.Context, tx pgx.Tx, steps []dbgen.PipelineStep) error {
	type groupKey struct{ queue, kind, providerRef string }
	groups := make(map[groupKey][]dbgen.PipelineStep)
	for _, s := range steps {
		if s.Status != StatusQueued {
			continue
		}
		key := groupKey{queue: s.Queue, kind: s.Kind, providerRef: s.ProviderRef}
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
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].providerRef < keys[j].providerRef
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
