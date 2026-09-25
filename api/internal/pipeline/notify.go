package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// NotifyChannel is the single Postgres NOTIFY channel the SSE hub LISTENs
// on. Every payload carries only ids, numbers and versions, never prompt
// text or user content, and stays well under Postgres's 8KB NOTIFY limit
// (in practice well under 1KB).
const NotifyChannel = "lt_events"

// stepEvent is the NOTIFY payload for a step change. Transition is true
// for every state change (queued->running, ->done, ->failed, ->canceled)
// and false for a throttled progress-only update; the SSE hub uses it to
// decide what it is allowed to coalesce or drop under load.
type stepEvent struct {
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

// execer is the minimal subset of dbgen.DBTX notify needs; satisfied by
// both *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// publishStepEvent sends a NOTIFY for step on NotifyChannel. It logs and
// swallows a failure rather than returning it: a dropped notification
// only delays an SSE client's next refresh (it will still catch up on its
// next snapshot fetch, or on the periodic reconnect/resync path), and
// must never fail the database transaction that already committed the
// real state change.
func publishStepEvent(ctx context.Context, db execer, step dbgen.PipelineStep, transition bool) {
	evt := stepEvent{
		Type:       "step",
		StepID:     idconv.FromPg(step.ID).String(),
		RunID:      idconv.FromPg(step.RunID).String(),
		TenantID:   idconv.FromPg(step.TenantID).String(),
		Status:     step.Status,
		Version:    step.Version,
		Transition: transition,
		Progress:   step.Progress,
	}
	if step.EtaS.Valid {
		evt.EtaS = step.EtaS.Int32
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline: failed to marshal step event", "error", err)
		return
	}
	if _, err := db.Exec(ctx, "SELECT pg_notify($1, $2)", NotifyChannel, string(payload)); err != nil {
		slog.WarnContext(ctx, "pipeline: failed to publish step event", "error", err)
	}
}
