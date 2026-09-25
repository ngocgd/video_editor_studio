package pipeline

import "testing"

var allStatuses = []string{StatusPending, StatusQueued, StatusRunning, StatusDone, StatusFailed, StatusCanceled}

// TestTransitionTableIsExhaustive walks every (from, to) pair over the
// known status set and asserts CanTransition agrees with a hand-checked
// expectation, so a future edit to the table cannot silently open an
// illegal move (e.g. done -> running) without a test failing.
func TestTransitionTableIsExhaustive(t *testing.T) {
	want := map[[2]string]bool{
		{StatusPending, StatusQueued}:   true,
		{StatusPending, StatusCanceled}: true,
		{StatusQueued, StatusRunning}:   true,
		{StatusQueued, StatusCanceled}:  true,
		{StatusRunning, StatusDone}:     true,
		{StatusRunning, StatusFailed}:   true,
		{StatusRunning, StatusQueued}:   true,
		{StatusRunning, StatusCanceled}: true,
		{StatusDone, StatusQueued}:      true,
		{StatusFailed, StatusQueued}:    true,
		{StatusCanceled, StatusQueued}:  true,
	}

	for _, from := range allStatuses {
		for _, to := range allStatuses {
			got := CanTransition(from, to)
			exp := want[[2]string{from, to}]
			if got != exp {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", from, to, got, exp)
			}
		}
	}
}

func TestCanTransitionRejectsUnknownFrom(t *testing.T) {
	if CanTransition("", StatusQueued) {
		t.Fatal("expected no legal transition from the empty/unknown status")
	}
	if CanTransition("bogus", StatusQueued) {
		t.Fatal("expected no legal transition from an unrecognised status")
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []string{StatusDone, StatusFailed, StatusCanceled} {
		if !Terminal(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	for _, s := range []string{StatusPending, StatusQueued, StatusRunning} {
		if Terminal(s) {
			t.Errorf("expected %q to not be terminal", s)
		}
	}
}

func TestLive(t *testing.T) {
	if !Live(StatusQueued) || !Live(StatusRunning) {
		t.Fatal("expected queued and running to be live")
	}
	if Live(StatusPending) || Live(StatusDone) || Live(StatusFailed) || Live(StatusCanceled) {
		t.Fatal("expected only queued/running to be live")
	}
}

func TestStale(t *testing.T) {
	if !Stale(StatusDone, "hash-a", "hash-b") {
		t.Fatal("expected a done step with a changed input hash to be stale")
	}
	if Stale(StatusDone, "hash-a", "hash-a") {
		t.Fatal("expected a done step with an unchanged input hash to not be stale")
	}
	if Stale(StatusRunning, "hash-a", "hash-b") {
		t.Fatal("expected staleness to only apply to done steps")
	}
}
