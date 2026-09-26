package models

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"loomtale/api/internal/pipeline"
)

func TestPullOutcomeTreatsOnlyCancellationAsPause(t *testing.T) {
	wrapped := fmt.Errorf("models: download a at byte 10: %w", context.Canceled)
	got := pullOutcome("m", context.Canceled, wrapped, 1)
	if !got.paused || got.markFailed || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled pull = %+v, want a pause that leaves the row alone", got)
	}
}

func TestPullOutcomeDeadlineRetriesThenFailsTheRowOnTheLastAttempt(t *testing.T) {
	installErr := fmt.Errorf("models: download a at byte 10: %w", context.DeadlineExceeded)

	early := pullOutcome("m", context.DeadlineExceeded, installErr, 1)
	if early.paused || early.markFailed {
		t.Fatalf("first timed-out attempt = %+v, want a retryable failure that keeps the row downloading", early)
	}
	if class, _ := pipeline.Classify(early.err); class != pipeline.ClassTransient {
		t.Fatalf("a timed-out attempt must stay transient so it is retried, got %v", class)
	}

	last := pullOutcome("m", context.DeadlineExceeded, installErr, pipeline.MaxTransientAttempts)
	if last.paused || !last.markFailed || !errors.Is(last.err, context.DeadlineExceeded) {
		t.Fatalf("last timed-out attempt = %+v, want the row marked failed so Install can resume it", last)
	}
}

func TestPullOutcomeDeadlineWithoutAnInstallErrorStillFails(t *testing.T) {
	got := pullOutcome("m", context.DeadlineExceeded, nil, pipeline.MaxTransientAttempts)
	if got.err == nil || !got.markFailed {
		t.Fatalf("deadline with a nil install error = %+v, want a failure", got)
	}
}

func TestPullOutcomePermanentErrorFailsTheRowAtOnce(t *testing.T) {
	got := pullOutcome("m", nil, ErrChecksumMismatch, 1)
	if got.paused || !got.markFailed {
		t.Fatalf("permanent error = %+v, want the row marked failed on the first attempt", got)
	}
}

func TestPullOutcomeSuccess(t *testing.T) {
	if got := pullOutcome("m", nil, nil, 1); got.paused || got.markFailed || got.err != nil {
		t.Fatalf("success = %+v, want a zero result", got)
	}
}
