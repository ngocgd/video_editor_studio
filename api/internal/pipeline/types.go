// Package pipeline is the generic job-orchestration engine: pipeline runs
// and steps live in Postgres, each step execution is one River job, and a
// per-step DB compare-and-set claim is the only fence against double
// execution. Phases 6-10 only register a pipeline.StepHandler per step
// kind; they never touch claiming, fan-in, retries or the GPU slot.
package pipeline

import (
	"context"
	"encoding/json"
	"time"

	dbgen "loomtale/api/internal/db/gen"

	"github.com/google/uuid"
)

// Priority values match River's own "lower runs first" ordering.
const (
	PriorityInteractive = 1 // writer AI actions, single regenerate
	PriorityScene        = 2 // per-scene reruns
	PriorityBatch         = 3 // batch generation
	PriorityTrainBench    = 4 // training and benchmarks
)

// Queue names. "gpu" is only enabled when WORKER_GPU=true.
const (
	QueueGPU    = "gpu"
	QueueCPU    = "cpu"
	QueueLLM    = "llm"
	QueueRender = "render"
	QueueIO     = "io"
)

// Step status values, matching the pipeline_steps.status CHECK constraint.
// "stale" is never stored: it is derived by comparing a done step's
// input_hash against the current one (see Stale).
const (
	StatusPending  = "pending"
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

// StepRef is the minimal identity of a step, passed to a StepHandler's
// Queue/InputHash/ModelRef methods before it is enqueued or resolved.
type StepRef struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	RunID     uuid.UUID
	ScopeKind string
	ScopeID   uuid.UUID
	Kind      string
	// Input is the step's enqueue-time input (StepSpec.Input), for a
	// handler whose hash depends on per-step parameters rather than on
	// its scope row alone. It is empty outside Enqueue.
	Input json.RawMessage
}

// ModelRef identifies a model a step needs resident on the GPU. Backend is
// the engine name (e.g. "comfyui", "ollama"); Model is its own identifier
// within that backend. Phase 4 and later fill in more detail through
// ModelResidency; phase 3 only ever compares these for equality.
type ModelRef struct {
	Backend string
	Model   string
}

// Output is a step's committed result, stored as pipeline_steps.output.
type Output map[string]any

// StepHandler is the single extension point every domain step (LLM,
// image, TTS, align, render, publish, analytics) implements. It never
// talks to River or the claim/fan-in machinery directly.
type StepHandler interface {
	// Kind returns the step kind this handler serves, matching
	// pipeline_steps.kind.
	Kind() string
	// Queue resolves which River queue a step of this kind runs on. It is
	// called once at enqueue time; the result is stored on the step row so
	// a later settings change never moves an already-queued step.
	Queue(ctx context.Context, s StepRef) (string, error)
	// InputHash returns a content hash of everything the step's output
	// depends on. A done step whose stored input_hash no longer matches
	// this value is stale (see Stale).
	InputHash(ctx context.Context, s StepRef) (string, error)
	// ModelRef returns the model this step needs resident on the GPU, or
	// nil if the step does not run on the GPU queue.
	ModelRef(ctx context.Context, s StepRef) (*ModelRef, error)
	// Run executes the step. Returning an error classified as gpu_oom or
	// transient by Classify lets the dispatcher retry; a permanent error
	// cancels the step.
	Run(ctx context.Context, sc *StepContext) (Output, error)
}

// AdmissionCheck runs inside Enqueue's own transaction, after it has
// taken the per-tenant admission lock (see LockTenantForAdmission), and
// before anything is inserted. q is that same transaction's Queries, so
// a check's own reads (e.g. counting a tenant's active steps) are
// serialized against concurrent Enqueue calls for the same tenant rather
// than racing a separate, unlocked connection. A non-nil error aborts
// the whole enqueue with no rows written; Engine.Enqueue maps it to a
// problem+json 429 or 507 response.
type AdmissionCheck func(ctx context.Context, q *dbgen.Queries, tenantID uuid.UUID, stepCount int) error

// GpuSnapshot is the live GPU state read by the /gpu endpoint and the
// render admission gate. Phase 4 is the only real implementation.
type GpuSnapshot struct {
	TotalMB        int64
	FreeMB         int64
	BudgetMB       int64
	RenderReserveMB int64
	MeasuredAt     time.Time
	Backends       []BackendStatus
	Encoder        *EncoderStatus
	Capabilities   []string
}

// BackendStatus reports one inference backend's reachability and what it
// currently has loaded.
type BackendStatus struct {
	Name      string
	Reachable bool
	Loaded    []string
}

// EncoderStatus reports the active video encoder.
type EncoderStatus struct {
	Name string
	HW   bool
}

// GpuProbe is implemented by phase 4 (real nvidia/backend polling); phase
// 3 only depends on this interface so the GPU executor and /gpu endpoint
// are testable with a fake.
type GpuProbe interface {
	Snapshot(ctx context.Context) (GpuSnapshot, error)
}

// ModelResidency is the interface pact with phase 4: it owns keeping a
// model loaded across steps and unloading everything on gpu_oom. Phase 3
// ships NoopResidency, used only when WORKER_GPU=false.
type ModelResidency interface {
	Ensure(ctx context.Context, m ModelRef) error
	UnloadAll(ctx context.Context) error
	Current() *ModelRef
}

// NoopResidency is the ModelResidency used when WORKER_GPU=false: the gpu
// queue is never enabled in that mode, so its methods should never be
// called, but they fail closed instead of panicking if they somehow are.
type NoopResidency struct{}

func (NoopResidency) Ensure(context.Context, ModelRef) error { return errGPUDisabled }
func (NoopResidency) UnloadAll(context.Context) error         { return errGPUDisabled }
func (NoopResidency) Current() *ModelRef                      { return nil }
