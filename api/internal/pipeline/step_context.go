package pipeline

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// progressThrottle is the minimum interval between progress writes for a
// single step, enforced in-process by StepContext.Progress.
const progressThrottle = time.Second

// StepContext is the handle a StepHandler.Run implementation uses to
// report progress, log, and read its own identity. It is created fresh
// per claimed step per attempt.
type StepContext struct {
	ctx      context.Context
	queries  *dbgen.Queries
	notifyDB execer
	step     dbgen.PipelineStep
	sink     logSink

	mu           sync.Mutex
	lastWrite    time.Time
	logs         *logRing
	lastProgress int16
	lastEtaS     int32
}

func newStepContext(ctx context.Context, queries *dbgen.Queries, notifyDB execer, step dbgen.PipelineStep, sink logSink) *StepContext {
	return &StepContext{
		ctx:      ctx,
		queries:  queries,
		notifyDB: notifyDB,
		step:     step,
		sink:     sink,
		logs:     newLogRing(),
	}
}

// Progress reports a step's completion percentage (0-100) and, if
// non-zero, its estimated remaining seconds. Writes are throttled to at
// most one per second: intermediate calls between writes are kept as the
// latest known value (used when the log is flushed) but never hit the
// database or NOTIFY.
func (sc *StepContext) Progress(pct int, etaS int) {
	sc.mu.Lock()
	sc.lastProgress = int16(pct)
	sc.lastEtaS = int32(etaS)
	sinceLast := time.Since(sc.lastWrite)
	shouldWrite := sinceLast >= progressThrottle
	if shouldWrite {
		sc.lastWrite = time.Now()
	}
	attempt := sc.step.Attempt
	stepID := sc.step.ID
	sc.mu.Unlock()

	if !shouldWrite {
		return
	}

	etaParam := idconv.ToPgInt4(int32(etaS))
	updated, err := sc.queries.UpdateStepProgress(sc.ctx, dbgen.UpdateStepProgressParams{
		Progress: int16(pct),
		EtaS:     etaParam,
		ID:       stepID,
		Attempt:  attempt,
	})
	if err != nil {
		// A zero-row update (step reclaimed) or a transient DB error both
		// leave the buffered lastProgress value in place; the dispatcher's
		// own heartbeat is the authoritative signal for "am I still the
		// owner", so a missed progress write is not itself fatal here.
		return
	}
	publishStepEvent(sc.ctx, sc.notifyDB, updated, false)
}

// Log appends a line to the step's in-memory, scrubbed log buffer. It is
// flushed to an asset when the step reaches a terminal state.
func (sc *StepContext) Log(line string) {
	sc.logs.add(line)
}

// Tenant returns the owning tenant id.
func (sc *StepContext) Tenant() uuid.UUID {
	return idconv.FromPg(sc.step.TenantID)
}

// ScopeKind returns the step's scope kind (e.g. "series", "episode"), set
// at enqueue time from StepSpec.ScopeKind. A StepHandler.Run
// implementation uses this together with ScopeID to load whatever domain
// row the step actually operates on.
func (sc *StepContext) ScopeKind() string {
	return sc.step.ScopeKind
}

// ScopeID returns the step's scope id, set at enqueue time from
// StepSpec.ScopeID.
func (sc *StepContext) ScopeID() uuid.UUID {
	return idconv.FromPg(sc.step.ScopeID)
}

// StepID returns this step's own id.
func (sc *StepContext) StepID() uuid.UUID {
	return idconv.FromPg(sc.step.ID)
}

// RunID returns the id of the pipeline run this step belongs to.
func (sc *StepContext) RunID() uuid.UUID {
	return idconv.FromPg(sc.step.RunID)
}

// Input returns the step's caller-supplied parameters set at enqueue
// time (StepSpec.Input), decoded into v. Called with an empty/absent
// input, this decodes "{}" into v (a no-op for a struct target).
func (sc *StepContext) Input(v any) error {
	data := sc.step.Input
	if len(data) == 0 {
		data = []byte("{}")
	}
	return json.Unmarshal(data, v)
}

// Attempt returns the current (1-based) attempt number for this step,
// matching pipeline_steps.attempt after the claiming CAS incremented it.
func (sc *StepContext) Attempt() int {
	return int(sc.step.Attempt)
}

// flushLog uploads the accumulated log buffer (if any) and returns the
// resulting asset id, or uuid.Nil if there was nothing to flush or no
// sink is configured (e.g. in a unit test).
func (sc *StepContext) flushLog(ctx context.Context) uuid.UUID {
	if sc.sink == nil {
		return uuid.Nil
	}
	body := sc.logs.bytes()
	if len(body) == 0 {
		return uuid.Nil
	}
	assetID, err := sc.sink.FlushLog(ctx, idconv.FromPg(sc.step.TenantID).String(), idconv.FromPg(sc.step.ID).String(), sc.step.Attempt, body)
	if err != nil || assetID == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(assetID)
	if err != nil {
		return uuid.Nil
	}
	return id
}
