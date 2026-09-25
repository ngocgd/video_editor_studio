package sse

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestSubscriber(t *testing.T) (*Subscriber, context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sub := newSubscriber(uuid.New(), uuid.New(), map[string]bool{"run-1": true})
	sub.TenantID = uuid.New()
	go sub.run(ctx)
	return sub, ctx, cancel
}

func readFrame(t *testing.T, sub *Subscriber, timeout time.Duration) frame {
	t.Helper()
	select {
	case f, ok := <-sub.out:
		if !ok {
			t.Fatal("subscriber output channel closed unexpectedly")
		}
		return f
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a frame")
	}
	return frame{}
}

func TestSubscriberSendsReadyFirst(t *testing.T) {
	sub, _, cancel := newTestSubscriber(t)
	defer cancel()

	f := readFrame(t, sub, time.Second)
	if f.Event != sseEventReady {
		t.Fatalf("expected the first frame to be %q, got %q", sseEventReady, f.Event)
	}
}

func TestSubscriberNeverDropsTransitions(t *testing.T) {
	sub, _, cancel := newTestSubscriber(t)
	defer cancel()
	_ = readFrame(t, sub, time.Second) // ready

	const n = transitionQueueSize // exactly fills the queue, no overflow
	tenantID := sub.TenantID.String()
	for i := 0; i < n; i++ {
		sub.deliver(Event{
			Type: "step", StepID: strconv.Itoa(i), RunID: "run-1", TenantID: tenantID,
			Status: "done", Version: int64(i), Transition: true,
		})
	}

	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		f := readFrame(t, sub, 2*time.Second)
		if f.Event != sseEventStep {
			t.Fatalf("expected a step event, got %q", f.Event)
		}
		seen[f.Data.StepID] = true
	}
	if len(seen) != n {
		t.Fatalf("expected all %d transitions delivered exactly once, got %d distinct", n, len(seen))
	}
}

func TestSubscriberCoalescesProgressPerKey(t *testing.T) {
	sub, _, cancel := newTestSubscriber(t)
	defer cancel()
	_ = readFrame(t, sub, time.Second) // ready

	tenantID := sub.TenantID.String()
	for pct := 0; pct <= 90; pct += 10 {
		sub.deliver(Event{Type: "step", StepID: "s1", RunID: "run-1", TenantID: tenantID, Status: "running", Progress: int16(pct)})
	}

	f := readFrame(t, sub, time.Second)
	if f.Event != sseEventStep || f.Data.StepID != "s1" {
		t.Fatalf("expected a coalesced progress frame for s1, got %+v", f)
	}
	if f.Data.Progress != 90 {
		t.Fatalf("expected the coalesced frame to carry the latest progress value 90, got %d", f.Data.Progress)
	}
}

func TestSubscriberIgnoresEventsOutsideItsTopicsOrTenant(t *testing.T) {
	sub, _, cancel := newTestSubscriber(t)
	defer cancel()
	_ = readFrame(t, sub, time.Second) // ready

	if sub.allows(Event{TenantID: sub.TenantID.String(), RunID: "other-run"}) {
		t.Fatal("expected an unsubscribed run id to be rejected")
	}
	if sub.allows(Event{TenantID: uuid.NewString(), RunID: "run-1"}) {
		t.Fatal("expected an event from a different tenant to be rejected")
	}
	if !sub.allows(Event{TenantID: sub.TenantID.String(), RunID: "run-1"}) {
		t.Fatal("expected a same-tenant, subscribed-topic event to be allowed")
	}
}

func TestSubscriberOverflowForcesResync(t *testing.T) {
	// Deliberately does not start run() (via newTestSubscriber) before
	// overfilling: run's own select loop drains s.transitions
	// concurrently, which would race the overfill and could drain it
	// fast enough that the channel never actually reaches capacity,
	// making the test flaky. Filling the unbuffered-consumer channel
	// first, synchronously, guarantees the overflow this test exists to
	// check actually happens before anything is running to prevent it.
	sub := newSubscriber(uuid.New(), uuid.New(), map[string]bool{"run-1": true})
	tenantID := sub.TenantID.String()
	for i := 0; i < transitionQueueSize+10; i++ {
		sub.deliver(Event{Type: "step", StepID: strconv.Itoa(i), RunID: "run-1", TenantID: tenantID, Transition: true})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx)

	sawResync := false
	deadline := time.After(3 * time.Second)
	for !sawResync {
		select {
		case f := <-sub.out:
			if f.Event == sseEventResync {
				sawResync = true
			}
		case <-deadline:
			t.Fatal("expected a resync frame after the transition queue overflowed")
		}
	}
}
