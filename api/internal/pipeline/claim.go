package pipeline

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// Claim runs the only fence in the system: it atomically moves every
// step in ids from "queued" to "running", bumping its attempt counter,
// and returns exactly the steps it actually claimed. A step already
// claimed by a concurrent job (River rescue racing the reconciler, or a
// step that was cancelled) is simply absent from the result; the caller
// must never write output for an id that is not in the returned slice.
func Claim(ctx context.Context, q *dbgen.Queries, jobID int64, ids []uuid.UUID) ([]dbgen.PipelineStep, error) {
	return q.ClaimSteps(ctx, dbgen.ClaimStepsParams{
		JobID: pgtype.Int8{Int64: jobID, Valid: true},
		Ids:   toPgUUIDs(ids),
	})
}

// Heartbeat extends a claimed step's liveness. It returns false when zero
// rows were updated, meaning the step's attempt no longer matches (it was
// reclaimed by the reconciler after a missed heartbeat window): the
// caller must cancel its own context and stop working immediately without
// committing anything.
func Heartbeat(ctx context.Context, q *dbgen.Queries, id uuid.UUID, attempt int32) (bool, error) {
	rows, err := q.HeartbeatStep(ctx, dbgen.HeartbeatStepParams{ID: idconv.ToPg(id), Attempt: attempt})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
