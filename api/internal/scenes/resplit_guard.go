package scenes

import (
	"errors"
	"fmt"
)

// ErrDropsWork is returned when a re-split would delete scenes that a
// person edited or that hold takes, and the request did not confirm it.
// Such work (hand-fixed speakers, prompts, generated takes) cannot be
// restored, so it is never deleted silently.
var ErrDropsWork = errors.New("scenes: the split would delete edited scenes or takes")

// ExistingScene is what a re-split needs to know about a current scene.
type ExistingScene struct {
	TextHash  string
	Edited    bool
	TakeCount int
}

// DropRisk counts what a re-split deletes: every scene no draft keeps,
// how many of those a person edited, and the takes they hold.
type DropRisk struct {
	Dropped, Edited, Takes int
}

// LosesWork reports whether the deleted scenes carry anything a person
// would have to redo; plain generated scenes without takes do not.
func (r DropRisk) LosesWork() bool { return r.Edited > 0 || r.Takes > 0 }

// Message is the user-facing sentence for a confirmation prompt.
// upperBound is set when the drafts are not known yet (an LLM split), so
// the counts cover every current scene.
func (r DropRisk) Message(upperBound bool) string {
	verb := "will delete"
	if upperBound {
		verb = "may delete up to"
	}
	return fmt.Sprintf("Splitting again %s %d scenes, including %d edited scenes and %d takes. This cannot be undone.", verb, r.Dropped, r.Edited, r.Takes)
}

// RiskOf counts the scenes that plan (from PlanResplit) does not keep.
func RiskOf(existing []ExistingScene, plan []int) DropRisk {
	kept := make([]bool, len(existing))
	for _, k := range plan {
		if k >= 0 {
			kept[k] = true
		}
	}
	var r DropRisk
	for i, sc := range existing {
		if kept[i] {
			continue
		}
		r.Dropped++
		if sc.Edited {
			r.Edited++
		}
		r.Takes += sc.TakeCount
	}
	return r
}

// RiskOfAll is the upper bound when the drafts are not known yet: any
// current scene may be dropped.
func RiskOfAll(existing []ExistingScene) DropRisk {
	return RiskOf(existing, nil)
}

// DropsWorkError carries the counts of a refused re-split; it matches
// ErrDropsWork with errors.Is.
type DropsWorkError struct {
	Risk       DropRisk
	UpperBound bool
}

func (e *DropsWorkError) Error() string { return e.Risk.Message(e.UpperBound) }

func (e *DropsWorkError) Is(target error) bool { return target == ErrDropsWork }
