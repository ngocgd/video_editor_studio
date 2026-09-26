//go:build integration

// This file drives GET /api/v1/events through the real, running stack —
// Caddy (deploy/caddy/Caddyfile) fronting the api container — instead of
// api/internal/sse.Hub directly, so it is the one place that actually
// proves gzip/buffering never sits between the API and a browser's
// EventSource, and that cross-tenant rejection holds at the HTTP layer,
// not just inside the Go package.
package integration

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// streamClient has no per-request timeout (httpTimeout, used by every
// other test's session, would cut an SSE connection off after 10s
// regardless of how much is still to observe); the test itself bounds
// how long it waits for each event instead.
var streamClient = &http.Client{}

// openEventStream issues GET /events?topics=... using sess's own session
// cookie (matching sess.do's auth, without its httpTimeout-bounded
// client) and returns the live response for the caller to read
// incrementally.
func openEventStream(t *testing.T, sess *session, topics string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL()+"/events?topics="+topics, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.cookie})
	resp, err := streamClient.Do(req)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	return resp
}

// readLineWithDeadline reads one line, failing the test if none arrives
// within timeout, used to prove events arrive incrementally rather than
// only once the whole response finally closes.
func readLineWithDeadline(t *testing.T, r *bufio.Reader, timeout time.Duration) (string, time.Duration) {
	t.Helper()
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	start := time.Now()
	go func() {
		line, err := r.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		return line, time.Since(start)
	case err := <-errCh:
		t.Fatalf("read line: %v", err)
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a line from the event stream")
	}
	return "", 0
}

// TestStreamEventsThroughCaddyDeliversIncrementallyAndRejectsOtherTenant
// covers decision items 8/9: a real HTTP client, through the real Caddy
// config, sees "ready" arrive immediately and a step transition arrive
// noticeably later (proving nothing buffered the whole response until
// close — the gzip/flush fix in deploy/caddy/Caddyfile), and a different
// tenant's session requesting the same run id as a topic is rejected.
func TestStreamEventsThroughCaddyDeliversIncrementallyAndRejectsOtherTenant(t *testing.T) {
	skipIfAPIUnreachable(t)
	ownerQ := gen.New(ownerPool(t))
	fxA := createFixtureUser(t, ownerQ, "sse-http-tenant-a", uniqueEmail("sse-http-a"), "viewer")

	registry := pipeline.NewRegistry()
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	registry.Register(&fakeHandler{kind: "sse-http-step", queue: testQueue, run: func(context.Context, *pipeline.StepContext) (pipeline.Output, error) {
		close(handlerStarted)
		<-releaseHandler
		return pipeline.Output{}, nil
	}})
	engine, _ := pipelineEngine(t, registry)

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := engine.Enqueue(context.Background(), fxA.TenantID, pipeline.RunSpec{
		ID: runID, ScopeKind: "test", ScopeID: runID, Kind: "sse-http-test",
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: "sse-http-step", ScopeKind: "test", ScopeID: runID, Priority: pipeline.PriorityInteractive}},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	sessA := login(t, fxA.Email, fxA.Password)
	respA := openEventStream(t, sessA, runID.String())
	defer func() { _ = respA.Body.Close() }()
	requireStatus(t, respA, http.StatusOK)
	if ct := respA.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected a text/event-stream response, got Content-Type %q", ct)
	}
	if enc := respA.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("expected the SSE response to never be gzip-encoded (it would buffer), got Content-Encoding %q", enc)
	}

	readerA := bufio.NewReader(respA.Body)
	readyLine, readyLatency := readLineWithDeadline(t, readerA, 3*time.Second)
	if !strings.HasPrefix(readyLine, "event: ready") {
		t.Fatalf("expected the first line to be the ready event, got %q", readyLine)
	}
	t.Logf("ready event arrived after %s", readyLatency)

	// Dispatch the step through this test's own engine (a separate
	// process's pool, exactly like a real worker container): the live
	// api container's own SSE hub must observe the resulting NOTIFY and
	// forward it, proving delivery genuinely goes through Postgres
	// LISTEN/NOTIFY end to end, not just within one process.
	go func() { _ = engine.Dispatch(context.Background(), 91001, []uuid.UUID{stepID}, pipeline.DispatchOpts{}) }()
	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatched handler never started")
	}
	close(releaseHandler)

	deadline := time.Now().Add(10 * time.Second)
	var sawDoneTransition bool
	for time.Now().Before(deadline) && !sawDoneTransition {
		line, latency := readLineWithDeadline(t, readerA, 10*time.Second)
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"status":"done"`) {
			sawDoneTransition = true
			t.Logf("done transition line arrived %s after the ready event (proves incremental delivery, not buffered until close)", latency+readyLatency)
		}
	}
	if !sawDoneTransition {
		t.Fatal("never saw the step's done transition on tenant A's stream")
	}

	// A session scoped to any tenant other than the run's owner must never
	// subscribe to it. Rather than a second user and login, rotate this
	// same session onto a second tenant fxA also belongs to
	// via /auth/switch-tenant: once switched, its active tenant no longer
	// owns runID, which is exactly as genuine a cross-tenant boundary as
	// a second, wholly unrelated user would be.
	tenantB, err := ownerQ.CreateTenant(context.Background(), gen.CreateTenantParams{ID: idconv.ToPg(idconv.NewV7()), Name: "sse-http-tenant-b"})
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	if _, err := ownerQ.CreateMembership(context.Background(), gen.CreateMembershipParams{
		TenantID: tenantB.ID, UserID: idconv.ToPg(fxA.UserID), Role: "viewer",
	}); err != nil {
		t.Fatalf("add second membership: %v", err)
	}
	switchResp := sessA.do(http.MethodPost, "/auth/switch-tenant", map[string]string{"tenantId": idconv.FromPg(tenantB.ID).String()})
	requireStatus(t, switchResp, http.StatusOK)
	var switched struct {
		CsrfToken string `json:"csrfToken"`
	}
	decodeJSON(t, switchResp, &switched)
	for _, c := range switchResp.Cookies() {
		if c.Name == sessionCookieName {
			sessA.cookie = c.Value
		}
	}
	sessA.csrfToken = switched.CsrfToken

	respB := openEventStream(t, sessA, runID.String())
	defer func() { _ = respB.Body.Close() }()
	requireStatus(t, respB, http.StatusForbidden)
}
