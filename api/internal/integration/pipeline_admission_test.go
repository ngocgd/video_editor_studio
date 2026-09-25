//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/quota"
)

// TestQuotaCheckEnforcesConfiguredLimit asserts a tenant with a
// tenant_quotas row is rejected once its active step count would exceed
// max_active_steps, and that a tenant with no row at all (the local
// default) is unlimited.
func TestQuotaCheckEnforcesConfiguredLimit(t *testing.T) {
	skipIfAPIUnreachable(t)
	pool := appPool(t)
	q := dbgen.New(pool)
	ownerP := ownerPool(t)
	tenantID := pipelineFixtureTenant(t, dbgen.New(ownerP), "quota-tenant")

	checker := &quota.Checker{}

	// No tenant_quotas row yet: unlimited.
	if err := checker.Check(context.Background(), q, tenantID, 1000); err != nil {
		t.Fatalf("expected an unquotaed tenant to be unlimited, got %v", err)
	}

	if _, err := ownerP.Exec(context.Background(),
		`INSERT INTO tenant_quotas (tenant_id, max_active_steps) VALUES ($1, 2)`, tenantID,
	); err != nil {
		t.Fatalf("seed quota: %v", err)
	}

	if err := checker.Check(context.Background(), q, tenantID, 2); err != nil {
		t.Fatalf("expected exactly-at-limit to be allowed, got %v", err)
	}
	err := checker.Check(context.Background(), q, tenantID, 3)
	if !errors.Is(err, pipeline.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded once the request would exceed the limit, got %v", err)
	}
}

// TestEnqueueRejectsOverQuotaRunWithNoRowsWritten asserts a run that
// fails admission never creates the run row or any step: Enqueue's
// admission checks run before the transaction starts anything.
func TestEnqueueRejectsOverQuotaRunWithNoRowsWritten(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("admission-step", pipeline.QueueCPU))
	pool := appPool(t)
	ownerP := ownerPool(t)
	tenantID := pipelineFixtureTenant(t, dbgen.New(ownerP), "admission-tenant")

	if _, err := ownerP.Exec(context.Background(),
		`INSERT INTO tenant_quotas (tenant_id, max_active_steps) VALUES ($1, 0)`, tenantID,
	); err != nil {
		t.Fatalf("seed quota: %v", err)
	}

	checker := &quota.Checker{}
	engine, _ := pipelineEngineWithChecks(t, registry, []pipeline.AdmissionCheck{checker.Check})

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	_, err := engine.Enqueue(context.Background(), tenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "admission-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "admission-step", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityBatch}},
	})
	if !errors.Is(err, pipeline.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	var runCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pipeline_runs WHERE id = $1`, runID).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 0 {
		t.Fatal("expected the rejected run to never have been written")
	}
}
