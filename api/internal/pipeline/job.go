package pipeline

import "github.com/google/uuid"

// JobKind is the single River job kind every pipeline step runs under.
// The dispatcher looks up the real per-step handler by the step's own
// "kind" column (stored in Postgres, never in job args), so adding a new
// step type never adds a new River job kind.
const JobKind = "pipeline_step"

// StepJobArgs is the only data a River job carries: step ids. Everything
// else a handler needs (prompt text, tokens, provider settings) lives in
// Postgres and is read back inside the claimed transaction, so a step
// cannot be re-run with stale or forged arguments smuggled through the
// job queue.
type StepJobArgs struct {
	StepIDs []uuid.UUID `json:"step_ids"`
}

// Kind implements river.JobArgs.
func (StepJobArgs) Kind() string { return JobKind }

// stepJobMetadata is stored in River's own InsertOpts.Metadata (never in
// Args, which stays step ids only). It exists purely so
// StepWorker.Timeout can resolve a per-kind timeout override without a
// database round trip: River's Tags field would be the more obvious
// place, but Tags are restricted to a narrow \w[\w-]+\w pattern that
// rejects the dotted step kinds this codebase uses (e.g. "train.lora").
type stepJobMetadata struct {
	Kind string `json:"kind"`
}
