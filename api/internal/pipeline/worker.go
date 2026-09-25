package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/riverqueue/river"
)

// QueueTimeouts is the JobTimeout default per queue, applied by
// StepWorker.Timeout when no per-kind override matches.
var QueueTimeouts = map[string]time.Duration{
	QueueGPU:    30 * time.Minute, // sized to one ~10-min batch chunk plus headroom
	QueueRender: 3 * time.Hour,
	QueueIO:     2 * time.Hour,
	QueueCPU:    30 * time.Minute,
	QueueLLM:    10 * time.Minute,
}

// KindTimeoutOverrides overrides QueueTimeouts for specific step kinds
// (exact match) or kind prefixes (a key ending in ".", matched with
// strings.HasPrefix). Every value here must stay below
// RescueStuckJobsAfter; AssertTimeoutsBelowRescue checks this at startup.
var KindTimeoutOverrides = map[string]time.Duration{
	"train.lora": 2 * time.Hour,
	"bench.":     1 * time.Hour,
}

// RescueStuckJobsAfter is set well above the longest configured timeout
// so River's own stuck-job rescue never fires on a job that is still
// genuinely running; the per-step DB CAS (not this value) is the fence
// that makes a concurrent rescue-plus-reconciler enqueue harmless either
// way.
const RescueStuckJobsAfter = 4 * time.Hour

// AssertTimeoutsBelowRescue fails fast at startup if any configured
// timeout is not strictly less than RescueStuckJobsAfter, which would
// let River consider a still-running job "stuck" while it is still live.
func AssertTimeoutsBelowRescue() error {
	for queue, d := range QueueTimeouts {
		if d >= RescueStuckJobsAfter {
			return fmt.Errorf("pipeline: queue %q timeout %s is not below RescueStuckJobsAfter %s", queue, d, RescueStuckJobsAfter)
		}
	}
	for kind, d := range KindTimeoutOverrides {
		if d >= RescueStuckJobsAfter {
			return fmt.Errorf("pipeline: kind %q timeout %s is not below RescueStuckJobsAfter %s", kind, d, RescueStuckJobsAfter)
		}
	}
	return nil
}

// StepWorker is the single river.Worker registered for pipeline_step
// jobs. It dispatches to the GPU executor for the gpu queue, gates
// render.* jobs on VRAM admission, and otherwise runs the engine's plain
// claim-and-dispatch path.
type StepWorker struct {
	river.WorkerDefaults[StepJobArgs]
	Engine          *Engine
	GPU             *GPUExecutor
	Probe           GpuProbe
	RenderReserveMB int64
	Sink            logSink
}

// Work implements river.Worker.
func (w *StepWorker) Work(ctx context.Context, job *river.Job[StepJobArgs]) error {
	switch job.Queue {
	case QueueGPU:
		return w.GPU.Run(ctx, job.ID, job.Attempt, job.MaxAttempts, job.Args.StepIDs, w.Sink)
	case QueueRender:
		if w.Probe != nil {
			admitted, err := RenderAdmitted(ctx, w.Probe, w.RenderReserveMB)
			if err != nil {
				return err
			}
			if !admitted {
				return river.JobSnooze(15 * time.Second)
			}
		}
	}
	return w.Engine.Dispatch(ctx, job.ID, job.Args.StepIDs, DispatchOpts{Sink: w.Sink, RiverAttempt: job.Attempt, RiverMaxAttempts: job.MaxAttempts})
}

// Timeout implements river.Worker: it resolves a per-kind override (read
// from the job's Metadata, since job args carry only step ids) falling
// back to the per-queue default.
func (w *StepWorker) Timeout(job *river.Job[StepJobArgs]) time.Duration {
	kind := kindFromMetadata(job.Metadata)
	if d, ok := KindTimeoutOverrides[kind]; ok {
		return d
	}
	for prefix, d := range KindTimeoutOverrides {
		if strings.HasSuffix(prefix, ".") && strings.HasPrefix(kind, prefix) {
			return d
		}
	}
	if d, ok := QueueTimeouts[job.Queue]; ok {
		return d
	}
	return 0 // inherit the client-level default
}

func kindFromMetadata(raw []byte) string {
	var meta stepJobMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return meta.Kind
}
