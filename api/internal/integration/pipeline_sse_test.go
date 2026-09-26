//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/sse"
)

// sseFrame is the test-side decode of one SSE record, read off
// Subscriber.WriteTo's real wire format: the same bytes a browser's
// EventSource would see.
type sseFrame struct {
	Event string
	Data  sse.Event
}

// sseReader parses frames from sub.WriteTo, run on a background
// goroutine feeding an io.Pipe, exactly like pipelineapi.StreamEvents
// does in production.
type sseReader struct {
	frames chan sseFrame
}

func startSSEReader(t *testing.T, sub *sse.Subscriber) *sseReader {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		_, err := sub.WriteTo(pw)
		_ = pw.CloseWithError(err)
	}()

	r := &sseReader{frames: make(chan sseFrame, 64)}
	go func() {
		scanner := bufio.NewScanner(pr)
		var eventName string
		var dataLine string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				eventName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				dataLine = strings.TrimPrefix(line, "data: ")
			case line == "" && eventName != "":
				var evt sse.Event
				_ = json.Unmarshal([]byte(dataLine), &evt)
				r.frames <- sseFrame{Event: eventName, Data: evt}
				eventName, dataLine = "", ""
			default:
				// A ": ..." heartbeat comment line (or anything else
				// unrecognised) carries no event data; ignored.
			}
		}
	}()
	return r
}

// waitForStepStatus reads frames until one matches stepID at status, or
// timeout elapses. Real SSE delivery is only ordered, not synchronized
// with subscription start, so a caller must be prepared to see other
// (still valid) events for the same step first.
func waitForStepStatus(t *testing.T, r *sseReader, stepID, status string, timeout time.Duration) sseFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f := r.next(t, timeout)
		if f.Event == "step" && f.Data.StepID == stepID && f.Data.Status == status {
			return f
		}
	}
	t.Fatalf("timed out waiting for step %s to reach status %q", stepID, status)
	return sseFrame{}
}

func (r *sseReader) next(t *testing.T, timeout time.Duration) sseFrame {
	t.Helper()
	select {
	case f := <-r.frames:
		return f
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an SSE frame")
	}
	return sseFrame{}
}

// TestSSESubscribeReadyThenTransitionsDeliveredAndTenantIsolated drives a
// real Hub against a live Postgres LISTEN connection: subscribe, receive
// ready, then complete a step through the real dispatch path (which
// NOTIFYs through the same channel the hub listens on) and assert the
// transition event arrives with the right run/tenant. A second
// subscriber for a different tenant, subscribed to the same run id
// string (as if authorization had been bypassed), must never receive it.
func TestSSESubscribeReadyThenTransitionsDeliveredAndTenantIsolated(t *testing.T) {
	skipIfAPIUnreachable(t)
	registry := pipeline.NewRegistry()
	registry.Register(succeedsImmediately("sse-step", testQueue))
	engine, _ := pipelineEngine(t, registry)
	q := ownerQueries(t)
	tenantA := pipelineFixtureTenant(t, q, "sse-tenant-a")
	tenantB := pipelineFixtureTenant(t, q, "sse-tenant-b")

	appName := "sse-transitions-test-" + uuid.NewString()
	hub := sse.NewHub(markedPool(t, appName))
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go hub.Run(hubCtx)
	waitForListen(t, appName)

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), tenantA, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "sse-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "sse-step", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	subCtx, stopSub := context.WithCancel(context.Background())
	defer stopSub()
	subA, err := hub.Subscribe(subCtx, uuid.New(), tenantA, map[string]bool{runID.String(): true})
	if err != nil {
		t.Fatalf("subscribe A: %v", err)
	}
	subB, err := hub.Subscribe(subCtx, uuid.New(), tenantB, map[string]bool{runID.String(): true})
	if err != nil {
		t.Fatalf("subscribe B: %v", err)
	}
	readerA := startSSEReader(t, subA)
	readerB := startSSEReader(t, subB)

	if f := readerA.next(t, 2*time.Second); f.Event != "ready" {
		t.Fatalf("expected subscriber A's first frame to be ready, got %q", f.Event)
	}
	if f := readerB.next(t, 2*time.Second); f.Event != "ready" {
		t.Fatalf("expected subscriber B's first frame to be ready, got %q", f.Event)
	}

	if err := engine.Dispatch(context.Background(), 8001, []uuid.UUID{stepID}, pipeline.DispatchOpts{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// NOTIFY delivery is asynchronous with respect to Subscribe: a
	// subscriber can see an event for this step id that predates it (the
	// enqueue-time "queued" transition, still in flight through
	// Postgres). That is exactly why the real contract has clients
	// subscribe first and discard anything whose version does not move
	// them forward; this loop plays that same role instead of assuming
	// the very next frame is the one being waited for.
	f := waitForStepStatus(t, readerA, stepID.String(), pipeline.StatusDone, 5*time.Second)
	if f.Data.RunID != runID.String() {
		t.Fatalf("expected the done event to carry the run id, got %+v", f)
	}

	select {
	case leaked := <-readerB.frames:
		t.Fatalf("expected tenant B to receive nothing for tenant A's run, got %+v", leaked)
	case <-time.After(1 * time.Second):
	}
}

// TestSSEResyncOnListenLoss kills the hub's own LISTEN backend and
// asserts a connected subscriber receives a resync frame once the hub
// reconnects.
func TestSSEResyncOnListenLoss(t *testing.T) {
	skipIfAPIUnreachable(t)
	// A uniquely-named pool for this test's own Hub: the stack under CI
	// also runs the real api container's own SSE hub, LISTEN-ing on the
	// exact same channel, which a broad "any LISTEN backend" kill would
	// take down right along with this test's.
	appName := "sse-resync-test-" + uuid.NewString()
	pool := markedPool(t, appName)
	hub := sse.NewHub(pool)
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go hub.Run(hubCtx)
	waitForListen(t, appName)

	subCtx, stopSub := context.WithCancel(context.Background())
	defer stopSub()
	sub, err := hub.Subscribe(subCtx, uuid.New(), uuid.New(), map[string]bool{"any-run": true})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	reader := startSSEReader(t, sub)
	if f := reader.next(t, 2*time.Second); f.Event != "ready" {
		t.Fatalf("expected ready first, got %q", f.Event)
	}

	// pg_terminate_backend requires either superuser or the same role as
	// the target backend; markedPool connects as the app role (same DSN
	// as appPool), so the terminating connection must too.
	terminator := appPool(t)
	if _, err := terminator.Exec(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1 AND pid <> pg_backend_pid()`, appName,
	); err != nil {
		t.Fatalf("terminate LISTEN backend: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-reader.frames:
			if f.Event == "resync" {
				return
			}
		case <-deadline:
			t.Fatal("expected a resync frame after the LISTEN connection was killed and reconnected")
		}
	}
}

// waitForListen polls until this test's own Hub connection (identified by
// its application_name, see markedPool) shows up in pg_stat_activity, so
// a test never races the hub's own connection setup on a slow CI runner.
// It does not also match on query text: an idle connection's last-query
// text is not guaranteed to still read "LISTEN ..." the moment this
// polls, only that the connection itself is alive and registered.
func waitForListen(t *testing.T, appName string) {
	t.Helper()
	admin := ownerPool(t)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`, appName,
		).Scan(&n)
		if err == nil && n > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the hub's connection to appear")
}
