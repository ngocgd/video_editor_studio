package pipeline

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	dbgen "loomtale/api/internal/db/gen"
)

// Engine wires the database, River client, handler registry and admission
// checks together. cmd/api and cmd/worker each build one instance; tests
// build their own with a Registry containing only the fakes they need.
type Engine struct {
	Pool      *pgxpool.Pool
	Queries   *dbgen.Queries
	River     *river.Client[pgx.Tx]
	Registry  *Registry
	Checks    []AdmissionCheck
	Estimator StepEstimator
}

// NewEngine builds an Engine. estimator may be nil, in which case
// DefaultStepEstimate is used (one step per batch chunk for any kind
// without a registered estimate).
func NewEngine(pool *pgxpool.Pool, queries *dbgen.Queries, riverClient *river.Client[pgx.Tx], registry *Registry, checks []AdmissionCheck, estimator StepEstimator) *Engine {
	if estimator == nil {
		estimator = DefaultStepEstimate
	}
	return &Engine{
		Pool:      pool,
		Queries:   queries,
		River:     riverClient,
		Registry:  registry,
		Checks:    checks,
		Estimator: estimator,
	}
}
