//go:build integration

// Package integration's pipeline-* files exercise api/internal/pipeline
// directly against a live Postgres (not through the compiled api/worker
// binaries): phase 3 ships the generic engine but no real StepHandler, so
// there is nothing yet for a compiled server to run end to end through
// HTTP. Phases 6-10 add HTTP-level run/step tests once they register
// real handlers; this file's fakeHandler stands in for them here.
package integration

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// markedPool opens a pool whose every connection carries application_name
// = appName, so a test that needs to pg_terminate_backend a connection it
// itself opened (simulating a crashed lock or LISTEN connection) can
// target exactly that connection instead of any backend that happens to
// match a broader pattern like "every advisory lock holder" — which,
// against a stack also running the real api/worker containers, could
// just as easily kill one of theirs.
func markedPool(t *testing.T, appName string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		missingEnv(t, "DATABASE_URL", "needs the loomtale_app DSN of a running stack")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect with application_name %q: %v", appName, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// pipelineEngine builds a real pipeline.Engine against appPool(t), with an
// insert-only River client (tests call pipeline.Claim/Engine.Dispatch
// directly instead of running a live river.Client worker loop, since the
// CAS fence and fan-in logic under test live entirely in Postgres and in
// Engine, not in River's own fetch scheduling).
func pipelineEngine(t *testing.T, registry *pipeline.Registry) (*pipeline.Engine, *pgxpool.Pool) {
	t.Helper()
	return pipelineEngineOnPool(t, registry, appPool(t))
}

// pipelineEngineOnPool is pipelineEngine but against a caller-supplied
// pool (see markedPool), for tests that need to identify and target
// exactly this engine's own connections afterwards.
func pipelineEngineOnPool(t *testing.T, registry *pipeline.Registry, pool *pgxpool.Pool) (*pipeline.Engine, *pgxpool.Pool) {
	t.Helper()
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		RescueStuckJobsAfter: pipeline.RescueStuckJobsAfter,
	})
	if err != nil {
		t.Fatalf("build river client: %v", err)
	}
	queries := dbgen.New(pool)
	return pipeline.NewEngine(pool, queries, riverClient, registry, nil, nil), pool
}

// pipelineEngineWithChecks is pipelineEngine plus caller-supplied
// admission checks, for tests that exercise quota/admission rejection.
func pipelineEngineWithChecks(t *testing.T, registry *pipeline.Registry, checks []pipeline.AdmissionCheck) (*pipeline.Engine, *pgxpool.Pool) {
	t.Helper()
	pool := appPool(t)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		RescueStuckJobsAfter: pipeline.RescueStuckJobsAfter,
	})
	if err != nil {
		t.Fatalf("build river client: %v", err)
	}
	queries := dbgen.New(pool)
	return pipeline.NewEngine(pool, queries, riverClient, registry, checks, nil), pool
}

// fakeHandler is a minimal, fully real (not mocked-response) StepHandler:
// its Run function is supplied by the test, so it exercises the genuine
// claim/commit/fan-in path with test-controlled behavior.
type fakeHandler struct {
	kind  string
	queue string
	run   func(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error)
}

func (f *fakeHandler) Kind() string { return f.kind }
func (f *fakeHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return f.queue, nil
}
func (f *fakeHandler) InputHash(context.Context, pipeline.StepRef) (string, error) {
	return "fixed-hash", nil
}
func (f *fakeHandler) ModelRef(context.Context, pipeline.StepRef) (*pipeline.ModelRef, error) {
	return nil, nil
}
func (f *fakeHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	return f.run(ctx, sc)
}

func succeedsImmediately(kind, queue string) *fakeHandler {
	return &fakeHandler{kind: kind, queue: queue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		return pipeline.Output{"ok": true}, nil
	}}
}

// ownerQueries wraps ownerPool(t) in a *dbgen.Queries, matching the
// convenience the rest of this package uses through gen.New(pool).
func ownerQueries(t *testing.T) *dbgen.Queries {
	t.Helper()
	return dbgen.New(ownerPool(t))
}

// pipelineFixtureTenant creates a bare tenant row (no user/membership
// needed: these tests drive pipeline.Engine directly, never through
// session auth) and returns its id.
func pipelineFixtureTenant(t *testing.T, q *dbgen.Queries, name string) uuid.UUID {
	t.Helper()
	tenant, err := q.CreateTenant(context.Background(), dbgen.CreateTenantParams{ID: idconv.ToPg(idconv.NewV7()), Name: name})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return idconv.FromPg(tenant.ID)
}

// stepStatus reads a step's current status/attempt/remaining_deps
// straight from Postgres, bypassing Engine, to assert on ground truth
// independent of any bug in the engine's own read path.
type stepRow struct {
	Status        string
	Attempt       int32
	RemainingDeps int32
	Version       int64
}

func readStep(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) stepRow {
	t.Helper()
	var r stepRow
	err := pool.QueryRow(context.Background(),
		`SELECT status, attempt, remaining_deps, version FROM pipeline_steps WHERE id = $1`, id,
	).Scan(&r.Status, &r.Attempt, &r.RemainingDeps, &r.Version)
	if err != nil {
		t.Fatalf("read step %s: %v", id, err)
	}
	return r
}

// completeRiverJobsForStep marks any live river_job row for id as
// "completed", standing in for what a real river.Client would do to the
// job row once its Work call returns nil. Tests in this package drive
// pipeline.Engine.Dispatch directly instead of running a real
// river.Client fetch loop (the CAS fence and fan-in logic under test live
// entirely in Postgres and in Engine, not in River's own scheduling), so
// nothing else ever performs this transition; a test whose step is
// dispatched to completion and then later re-armed needs it explicitly,
// or its old, never-cleaned-up job row would be double-counted alongside
// the fresh one a re-arm inserts.
func completeRiverJobsForStep(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE river_job SET state = 'completed', finalized_at = now() WHERE kind = $1 AND args -> 'step_ids' ? $2 AND state IN ('available', 'scheduled', 'running', 'retryable', 'pending')`,
		pipeline.JobKind, id.String(),
	); err != nil {
		t.Fatalf("mark river jobs completed for step %s: %v", id, err)
	}
}

// countRiverJobsForStep counts live (non-terminal) river_job rows whose
// args mention id, used to assert "enqueued exactly once, never zero".
func countRiverJobsForStep(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = $1 AND args -> 'step_ids' ? $2 AND state IN ('available','scheduled','running','retryable')`,
		pipeline.JobKind, id.String(),
	).Scan(&n)
	if err != nil {
		t.Fatalf("count river jobs for step %s: %v", id, err)
	}
	return n
}

func setHeartbeatInPast(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, age time.Duration) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE pipeline_steps SET heartbeat_at = $1 WHERE id = $2`, time.Now().Add(-age), id)
	if err != nil {
		t.Fatalf("set heartbeat in past: %v", err)
	}
}

// forceStatus bypasses every fence to directly flip a step's status,
// simulating an external actor (the reconciler, a concurrent reclaim)
// without going through Engine, for tests that need to set up a
// precondition Engine itself would never produce on its own.
func forceStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `UPDATE pipeline_steps SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		t.Fatalf("force status: %v", err)
	}
}

// runConcurrently runs fn n times in parallel and waits for all of them.
func runConcurrently(n int, fn func(i int)) {
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// testQueue is the queue the engine tests put their fake kinds on. It must
// be a queue no live worker of the integration stack serves: that worker
// claims every job on the queues it enables and snoozes or strands a
// test-only kind it has no handler for, racing any test that runs its own
// River client or inspects River job state. The stack's worker serves llm,
// cpu and render (render steps run there); io is enabled only on a GPU
// worker with a models directory, which the integration stack never runs.
// The name must also pass the pipeline_steps.queue CHECK constraint, so it
// cannot be a made-up queue.
const testQueue = pipeline.QueueIO
