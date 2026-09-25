package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// progressCoalesceInterval bounds delivered progress updates to at most
// one per key (per step id) per interval: 250ms yields <=4/s/key.
const progressCoalesceInterval = 250 * time.Millisecond

// transitionQueueSize bounds how many transition events a slow consumer
// can have buffered before it is forced to resync instead of blocking
// the hub's fan-out goroutine indefinitely.
const transitionQueueSize = 256

// outQueueSize bounds the channel the HTTP handler reads rendered SSE
// frames from.
const outQueueSize = 256

// maxStreamLifetime bounds how long one /events connection stays open
// before it is ended (a resync-then-close, which EventSource's own
// browser-side reconnect will follow automatically): without this, a
// revoked session or a membership removed mid-stream keeps receiving
// events indefinitely, since nothing about an already-open SSE
// connection re-checks either. Reconnecting re-runs the handler's own
// auth and tenant-membership checks from scratch.
const maxStreamLifetime = 1 * time.Hour

// Subscriber is one open /events connection. It is created by Hub.Subscribe
// and driven by its own goroutine (run) until its context is cancelled
// (client disconnect) or the hub closes it (per-user cap eviction).
type Subscriber struct {
	ID       string
	UserID   uuid.UUID
	TenantID uuid.UUID
	Topics   map[string]bool // allowed run ids, as strings

	transitions chan Event
	resync      chan struct{}
	out         chan frame

	mu       sync.Mutex
	progress map[string]Event // latest undelivered progress per step id
}

// frame is a rendered SSE record: either a named event with a JSON data
// payload, or (Comment != "") a bare heartbeat comment line.
type frame struct {
	Event   string
	Data    Event
	Comment string
}

func newSubscriber(userID, tenantID uuid.UUID, topics map[string]bool) *Subscriber {
	return &Subscriber{
		ID:          uuid.NewString(),
		UserID:      userID,
		TenantID:    tenantID,
		Topics:      topics,
		transitions: make(chan Event, transitionQueueSize),
		resync:      make(chan struct{}, 1),
		out:         make(chan frame, outQueueSize),
		progress:    make(map[string]Event),
	}
}

// allows reports whether evt is in scope for this subscriber: same
// tenant (defence in depth on top of the hub only ever dispatching
// same-tenant events) and a topic (run id) the caller subscribed to.
func (s *Subscriber) allows(evt Event) bool {
	return evt.TenantID == s.TenantID.String() && s.Topics[evt.RunID]
}

// deliver routes evt to the transition queue or the progress coalescing
// map depending on evt.Transition. Called from the hub's fan-out
// goroutine; must never block.
func (s *Subscriber) deliver(evt Event) {
	if evt.Transition {
		// Drop any progress this step still has buffered before queueing
		// the transition: otherwise run's own select loop (which does
		// not guarantee ordering between the transitions channel and the
		// progress-flush ticker) could still flush a now-stale progress
		// value for this step after the terminal transition already
		// went out, which would visually revert a client's "done"/
		// "failed" display back to some earlier percentage.
		s.mu.Lock()
		delete(s.progress, evt.StepID)
		s.mu.Unlock()
		select {
		case s.transitions <- evt:
		default:
			// The transition queue overflowed: this subscriber is too far
			// behind to trust incremental delivery. Force a resync instead
			// of dropping a transition, which the contract never allows.
			s.forceResync()
		}
		return
	}
	s.mu.Lock()
	s.progress[evt.StepID] = evt
	s.mu.Unlock()
}

// forceResync clears buffered progress and asks run's loop to emit a
// resync frame; a resync tells the client to discard everything and
// refetch snapshots, so no queued progress/transition is meaningful
// anymore.
func (s *Subscriber) forceResync() {
	s.mu.Lock()
	s.progress = make(map[string]Event)
	s.mu.Unlock()
	// Drain any transitions queued before the overflow so the client
	// never sees stale ordering after the resync it is about to receive.
	for {
		select {
		case <-s.transitions:
		default:
			select {
			case s.resync <- struct{}{}:
			default:
			}
			return
		}
	}
}

// run drives frames onto s.out until ctx is cancelled.
func (s *Subscriber) run(ctx context.Context) {
	ticker := time.NewTicker(progressCoalesceInterval)
	defer ticker.Stop()
	defer close(s.out)

	s.send(ctx, frame{Event: sseEventReady})

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	maxLifetime := time.NewTimer(maxStreamLifetime)
	defer maxLifetime.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-maxLifetime.C:
			return
		case evt := <-s.transitions:
			s.send(ctx, frame{Event: sseEventStep, Data: evt})
		case <-s.resync:
			s.send(ctx, frame{Event: sseEventResync})
		case <-ticker.C:
			s.flushProgress(ctx)
		case <-heartbeat.C:
			s.send(ctx, frame{Comment: "heartbeat"})
		}
	}
}

func (s *Subscriber) flushProgress(ctx context.Context) {
	s.mu.Lock()
	pending := s.progress
	s.progress = make(map[string]Event)
	s.mu.Unlock()
	for _, evt := range pending {
		s.send(ctx, frame{Event: sseEventStep, Data: evt})
	}
}

func (s *Subscriber) send(ctx context.Context, f frame) {
	select {
	case s.out <- f:
	case <-ctx.Done():
	}
}

// WriteTo streams every frame this subscriber produces to w as
// text/event-stream bytes, until its context is cancelled (s.out is
// closed by run when that happens) or a write fails. It implements
// io.WriterTo so a caller (an http.Handler or, as in this codebase, a
// generated strict-server SSE response body) can drive it directly.
func (s *Subscriber) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for f := range s.out {
		b := formatFrame(f)
		n, err := w.Write(b)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func formatFrame(f frame) []byte {
	if f.Comment != "" {
		return []byte(fmt.Sprintf(": %s\n\n", f.Comment))
	}
	payload, err := json.Marshal(f.Data)
	if err != nil {
		return nil
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", f.Event, payload))
}
