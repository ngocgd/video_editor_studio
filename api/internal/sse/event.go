// Package sse implements the versioned, per-tenant server-sent events
// hub: one Postgres LISTEN connection per API instance fans pipeline step
// events out to HTTP subscribers, coalescing progress-only updates while
// never dropping a state transition.
package sse

// NotifyChannel is the Postgres NOTIFY channel pipeline step changes are
// published on (see api/internal/pipeline.NotifyChannel, which must stay
// equal to this value; the two packages do not import each other so a
// process wiring both together is the only place that would notice a
// drift, which is why it is asserted in hub_test.go against a literal).
const NotifyChannel = "lt_events"

// Event is the decoded NOTIFY payload. Field names and JSON tags mirror
// pipeline's private stepEvent exactly; sse never depends on the
// pipeline package so this is intentionally a separate, structurally
// identical type.
type Event struct {
	Type       string `json:"type"`
	StepID     string `json:"id"`
	RunID      string `json:"run_id"`
	TenantID   string `json:"tenant_id"`
	Status     string `json:"status"`
	Version    int64  `json:"version"`
	Transition bool   `json:"transition"`
	Progress   int16  `json:"progress,omitempty"`
	EtaS       int32  `json:"eta_s,omitempty"`
}

// sseEventType is the SSE wire "event:" field, distinct from Event.Type
// (the payload's own step/gpu classification).
const (
	sseEventReady  = "ready"
	sseEventResync = "resync"
	sseEventStep   = "step"
)
