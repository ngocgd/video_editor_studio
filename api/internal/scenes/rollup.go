package scenes

import (
	"strings"

	"github.com/google/uuid"
)

// Pip states, matching the storyboard's PipState enum.
const (
	StateDone    = "done"
	StateRunning = "running"
	StateQueued  = "queued"
	StateFailed  = "failed"
	StateStale   = "stale"
	StateNone    = "none"
)

// worstOrder ranks states for a scene's row-level status: the first one
// present wins (failed beats stale beats running, and so on).
var worstOrder = []string{StateFailed, StateStale, StateRunning, StateQueued, StateNone, StateDone}

// StepInfo is the latest pipeline step of one kind for a scene.
type StepInfo struct {
	ID        uuid.UUID `json:"id"`
	RunID     uuid.UUID `json:"runId"`
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	ErrorCode string    `json:"errorCode"`
	ErrorMsg  string    `json:"errorMsg"`
	Priority  int       `json:"priority"`
}

// TakeInfo is the selected take of one kind for a scene.
type TakeInfo struct {
	ID         uuid.UUID         `json:"id"`
	AssetID    uuid.UUID         `json:"assetId"`
	InputHash  string            `json:"inputHash"`
	Params     TakeParams        `json:"params"`
	Variants   map[string]any    `json:"variants"`
	DurationMs *int              `json:"durationMs"`
}

// TakeParams is the params jsonb of a take.
type TakeParams struct {
	Components Components     `json:"components"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// Pip is one kind's state for a scene.
type Pip struct {
	Kind        string
	State       string
	Step        *StepInfo
	StaleReason string
}

// PipFor derives a kind's pip: a live step wins (queued/running), then a
// failed latest step, then the selected take compared against the
// scene's current input hash.
func PipFor(kind string, step *StepInfo, take *TakeInfo, current Components) Pip {
	p := Pip{Kind: kind, Step: step}
	if step != nil {
		switch step.Status {
		case "pending", "queued":
			p.State = StateQueued
			return p
		case "running":
			p.State = StateRunning
			return p
		case "failed":
			p.State = StateFailed
			return p
		}
	}
	switch {
	case take == nil:
		p.State = StateNone
	case take.InputHash != current.Hash():
		p.State = StateStale
		p.StaleReason = StaleReason(kind, take.Params.Components, current)
	default:
		p.State = StateDone
	}
	return p
}

// WorstState is the row-level state of a set of pips.
func WorstState(states ...string) string {
	for _, candidate := range worstOrder {
		for _, s := range states {
			if s == candidate {
				return candidate
			}
		}
	}
	return StateNone
}

// Filter names, matching the SceneFilter enum.
const (
	FilterAll     = "all"
	FilterStale   = "stale"
	FilterFailed  = "failed"
	FilterMissing = "missing"
	FilterInQueue = "in_queue"
)

// Matches reports whether a scene whose generated kinds (image, voice,
// align) have these states belongs to filter.
func Matches(filter string, states []string) bool {
	has := func(want ...string) bool {
		for _, s := range states {
			for _, w := range want {
				if s == w {
					return true
				}
			}
		}
		return false
	}
	switch filter {
	case FilterStale:
		return has(StateStale)
	case FilterFailed:
		return has(StateFailed)
	case FilterMissing:
		return has(StateNone)
	case FilterInQueue:
		return has(StateQueued, StateRunning)
	default:
		return true
	}
}

// NeedsGeneration reports whether "Generate missing" should queue a kind
// in this state: nothing yet, out of date, or failed; never one already
// queued or running.
func NeedsGeneration(state string) bool {
	return state == StateNone || state == StateStale || state == StateFailed
}

// MatchesQuery is the storyboard's text search over the narration.
func MatchesQuery(q, narration string) bool {
	q = strings.TrimSpace(q)
	return q == "" || strings.Contains(foldName(narration), foldName(q))
}
