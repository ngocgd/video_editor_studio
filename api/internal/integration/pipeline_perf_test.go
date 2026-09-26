//go:build integration

package integration

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// enqueueBudget is the phase 3 performance budget: enqueueing 300 scene
// steps plus deps must finish in one transaction in <=150ms.
const enqueueBudget = 150 * time.Millisecond

// enqueueSamples is how many independent runs are enqueued; the median is
// compared to the budget so one scheduler hiccup on a shared CI runner
// does not fail the build, while a real regression still does.
const enqueueSamples = 5

// TestEnqueue300StepsWithDepsMeetsLatencyBudget seeds a two-stage DAG
// (150 leaf steps feeding 150 dependent steps, one dep each so the
// dependency-row count matches the leaf count) several times and asserts
// the median Enqueue latency is within budget.
func TestEnqueue300StepsWithDepsMeetsLatencyBudget(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("perf-leaf", testQueue))
	registry.Register(succeedsImmediately("perf-dependent", testQueue))
	engine, _ := pipelineEngine(t, registry)
	// A real per-scene stage registers a meaningful duration estimate
	// (phase 9), which is what actually bounds how many separate River
	// job inserts one Enqueue call needs; the package default
	// (DefaultStepEstimate, one chunk per step) exists to be safe for an
	// unknown kind, not to model a real batch's insert cost.
	engine.Estimator = func(string) time.Duration { return 30 * time.Second }
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "perf-tenant")
	// The enqueued runs are never worked; delete their tenant (cascading
	// to steps) so later tests' reconciler sweeps don't inherit them.
	t.Cleanup(func() {
		if _, err := ownerPool(t).Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID); err != nil {
			t.Errorf("cleanup perf-test tenant: %v", err)
		}
	})

	const pairs = 150 // 150 leaves + 150 dependents = 300 steps, 150 dep rows
	samples := make([]time.Duration, 0, enqueueSamples)
	for n := 0; n < enqueueSamples; n++ {
		runID := idconv.NewV7()
		steps := make([]pipeline.StepSpec, 0, pairs*2)
		for i := 0; i < pairs; i++ {
			leaf := idconv.NewV7()
			dependent := idconv.NewV7()
			steps = append(steps,
				pipeline.StepSpec{ID: leaf, Kind: "perf-leaf", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch},
				pipeline.StepSpec{ID: dependent, Kind: "perf-dependent", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch, DependsOn: []uuid.UUID{leaf}},
			)
		}

		start := time.Now()
		if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
			ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "perf-test", Steps: steps,
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		samples = append(samples, time.Since(start))
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	median := samples[len(samples)/2]
	t.Logf("enqueued %d steps x %d runs; latencies %v, median %s", pairs*2, enqueueSamples, samples, median)
	if median > enqueueBudget {
		t.Fatalf("median enqueue took %s, want <= %s", median, enqueueBudget)
	}
}

// TestProgressWritesAreThrottledToOncePerSecond drives StepContext.Progress
// far faster than once a second and asserts the version column (bumped
// exactly once per accepted write) moved at most twice: once for the
// claim's own commit path is irrelevant here (Progress writes happen
// mid-run, before commit), so the count reflects only accepted progress
// writes.
func TestProgressWritesAreThrottledToOncePerSecond(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(&fakeHandler{kind: "throttle-check", queue: testQueue, run: func(_ context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
		for i := 0; i < 50; i++ {
			sc.Progress(i, 0)
		}
		return pipeline.Output{}, nil
	}})
	engine, pool := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantID := pipelineFixtureTenant(t, q, "throttle-tenant")

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "throttle-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "throttle-check", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := engine.Dispatch(context.Background(), 9501, []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// version bumps once on claim, at most once for the 50 rapid Progress
	// calls (throttled to <=1 accepted write/s), and once on the final
	// commit: 3 total is the ceiling a working throttle allows. Without
	// throttling this would be 52 (claim + 50 progress writes + commit).
	row := readStep(t, pool, stepID)
	writes := int(row.Version)
	if writes > 3 {
		t.Fatalf("expected at most 3 version bumps (claim + <=1 throttled progress write + commit), got %d", writes)
	}
}
