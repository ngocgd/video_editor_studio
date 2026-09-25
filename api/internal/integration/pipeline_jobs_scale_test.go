//go:build integration

package integration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// jobsScaleRowCount matches the spec's GET /jobs performance budget: p95
// <=50ms with 50k steps seeded. Seeded at double that (>=100k) across two
// tenants so the target tenant's own share is still realistically
// selective, not "the whole table".
const jobsScaleRowCount = 60000

// TestListJobsUsesIndexesAndMeetsP95Budget seeds >=100k pipeline_steps
// rows (60k for the tenant under test, 60k for a second tenant sharing
// the table), asserts every ListJobs query variant (no filter,
// status-only, queue-only, both) actually uses one of the dedicated
// indexes via EXPLAIN rather than a sequential/full-index scan, and
// measures p95 latency over repeated calls against the spec's <=50ms
// budget.
func TestListJobsUsesIndexesAndMeetsP95Budget(t *testing.T) {
	skipIfAPIUnreachable(t)
	if testing.Short() {
		t.Skip("seeds 120k rows; skipped under -short")
	}
	registry := pipeline.NewRegistry()
	engine, pool := pipelineEngine(t, registry)
	ownerP := ownerPool(t)
	q := dbgen.New(ownerP)
	targetTenant := pipelineFixtureTenant(t, q, "jobs-scale-target")
	otherTenant := pipelineFixtureTenant(t, q, "jobs-scale-other")
	// Deleting the fixture tenants cascades through pipeline_runs ->
	// pipeline_steps (ON DELETE CASCADE, db/migrations/20260925020000_pipeline.sql):
	// this test seeds ~40k synthetic "queued" rows with no real River job
	// behind them (seedSteps bulk-inserts them directly, skipping Enqueue),
	// which is exactly what the reconciler's orphan sweep
	// (reconciler_orphans.go) is designed to catch. Left uncleaned, every
	// later test in this suite pays for reconciling tens of thousands of
	// individually-transacted rows, which is what made
	// TestReconcilerResumesAfterCrash time out downstream of this test.
	t.Cleanup(func() {
		if _, err := ownerP.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1)`,
			[]uuid.UUID{targetTenant, otherTenant}); err != nil {
			t.Errorf("cleanup seeded scale-test tenants: %v", err)
		}
	})

	seedSteps(t, pool, targetTenant, jobsScaleRowCount)
	seedSteps(t, pool, otherTenant, jobsScaleRowCount)
	// ANALYZE needs table-maintenance privilege the least-privilege app
	// role does not have; run it as owner so the planner's row estimates
	// reflect the rows just seeded instead of stale (or zero) stats.
	if _, err := ownerPool(t).Exec(context.Background(), "ANALYZE pipeline_steps"); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	t.Run("EXPLAIN uses an index for every filter combination", func(t *testing.T) {
		cases := []struct {
			name string
			sql  string
			args []any
		}{
			{"no filter", `SELECT * FROM pipeline_steps WHERE tenant_id = $1 AND id > $2 ORDER BY id LIMIT 50`, []any{targetTenant, idconv.NewV7()}},
			{"status only", `SELECT * FROM pipeline_steps WHERE tenant_id = $1 AND status = $2 AND id > $3 ORDER BY id LIMIT 50`, []any{targetTenant, "queued", idconv.NewV7()}},
			{"queue only", `SELECT * FROM pipeline_steps WHERE tenant_id = $1 AND queue = $2 AND id > $3 ORDER BY id LIMIT 50`, []any{targetTenant, "gpu", idconv.NewV7()}},
			{"status and queue", `SELECT * FROM pipeline_steps WHERE tenant_id = $1 AND status = $2 AND queue = $3 AND id > $4 ORDER BY id LIMIT 50`, []any{targetTenant, "queued", "gpu", idconv.NewV7()}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				plan := explain(t, pool, c.sql, c.args...)
				if strings.Contains(plan, "Seq Scan") {
					t.Fatalf("expected no sequential scan for %s, got plan:\n%s", c.name, plan)
				}
				if !strings.Contains(plan, "Index") {
					t.Fatalf("expected an index scan for %s, got plan:\n%s", c.name, plan)
				}
			})
		}
	})

	t.Run("p95 latency", func(t *testing.T) {
		const iterations = 60
		durations := make([]time.Duration, 0, iterations)
		for i := 0; i < iterations; i++ {
			start := time.Now()
			if _, err := engine.ListJobs(context.Background(), targetTenant, "", "", idconv.NewV7(), 50); err != nil {
				t.Fatalf("ListJobs: %v", err)
			}
			durations = append(durations, time.Since(start))
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		p95 := durations[int(float64(len(durations))*0.95)]
		t.Logf("GET /jobs p95 over %d calls against %d seeded rows: %s", iterations, jobsScaleRowCount*2, p95)
		if p95 > 50*time.Millisecond {
			t.Fatalf("p95 %s exceeds the 50ms budget", p95)
		}
	})
}

// explain runs EXPLAIN (not ANALYZE: seeded rows are synthetic and the
// planner's row estimates from a fresh ANALYZE would be what is actually
// under test, not execution time) and returns the plan text.
func explain(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN "+sql, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain line: %v", err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// seedSteps bulk-inserts n synthetic, already-terminal-mixed
// pipeline_steps rows for tenantID via COPY, under one throwaway run, so
// a scale test does not have to pay Enqueue's per-step handler
// resolution cost 100k+ times.
func seedSteps(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, n int) {
	t.Helper()
	runID := idconv.NewV7()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO pipeline_runs (id, tenant_id, scope_kind, scope_id, kind) VALUES ($1, $2, 'test', $1, 'scale-seed')`,
		runID, tenantID,
	); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	queues := []string{"gpu", "cpu", "llm", "render", "io"}
	statuses := []string{"pending", "queued", "running", "done", "failed", "canceled"}

	const batchSize = 5000
	for start := 0; start < n; start += batchSize {
		end := start + batchSize
		if end > n {
			end = n
		}
		rows := make([][]any, 0, end-start)
		for i := start; i < end; i++ {
			rows = append(rows, []any{
				idconv.NewV7(), tenantID, runID, "test", runID, "scale-seed-step",
				queues[i%len(queues)], "", 3, statuses[i%len(statuses)], 0, fmt.Sprintf("hash-%d", i),
			})
		}
		if _, err := pool.CopyFrom(context.Background(),
			pgx.Identifier{"pipeline_steps"},
			[]string{"id", "tenant_id", "run_id", "scope_kind", "scope_id", "kind", "queue", "provider_ref", "priority", "status", "remaining_deps", "input_hash"},
			pgx.CopyFromRows(rows),
		); err != nil {
			t.Fatalf("seed steps copy [%d,%d): %v", start, end, err)
		}
	}
}
