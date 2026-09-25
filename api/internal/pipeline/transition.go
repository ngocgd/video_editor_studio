package pipeline

// transitions is the exhaustive, pure state-transition table for
// pipeline_steps.status. It has no DB or River dependency so it can be
// tested in isolation; every other package in pipeline treats it as the
// single source of truth for "is this move legal".
var transitions = map[string]map[string]bool{
	StatusPending: {
		StatusQueued:   true, // remaining_deps reached 0
		StatusCanceled: true,
	},
	StatusQueued: {
		StatusRunning:  true, // claimed by CAS
		StatusCanceled: true,
	},
	StatusRunning: {
		StatusDone:     true,
		StatusFailed:   true,
		StatusQueued:   true, // reclaimed (stale heartbeat) or requeued (gpu_oom retry)
		StatusCanceled: true,
	},
	StatusDone: {
		StatusQueued: true, // manual retry re-runs a done-but-stale step
	},
	StatusFailed: {
		StatusQueued: true, // manual or transient-failure retry
	},
	StatusCanceled: {
		StatusQueued: true, // manual retry of a canceled step
	},
}

// CanTransition reports whether moving a step from "from" to "to" is a
// legal state change. An unknown "from" value (including "") is never
// legal to leave, so a step can only ever be created directly as pending.
func CanTransition(from, to string) bool {
	next, ok := transitions[from]
	if !ok {
		return false
	}
	return next[to]
}

// Terminal reports whether status is a terminal state: no further
// automatic processing (retry aside) happens to a step in this state.
func Terminal(status string) bool {
	switch status {
	case StatusDone, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

// Live reports whether status counts as "occupying a slot": used by
// CountActiveStepsForTenant-backed quota checks and the reconciler.
func Live(status string) bool {
	return status == StatusQueued || status == StatusRunning
}

// Stale reports whether a done step's output no longer reflects its
// current inputs: it is derived, never stored, by comparing the step's
// last-committed input_hash against a freshly computed one.
func Stale(status, storedInputHash, currentInputHash string) bool {
	return status == StatusDone && storedInputHash != currentInputHash
}
