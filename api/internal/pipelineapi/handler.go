// Package pipelineapi implements the jobs/runs/steps/gpu/events slice of
// the generated strict server interface: everything phase 3's HTTP
// surface exposes on top of api/internal/pipeline.Engine. StreamEvents
// (GET /events, see events.go) goes through the same generated strict
// interface as every other route here — oapi-codegen special-cases a
// text/event-stream response with a flush-per-chunk body — it just
// returns a hand-written io.Pipe-backed response instead of a single
// JSON value.
package pipelineapi

import (
	"context"
	"time"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/sse"
	"loomtale/api/internal/storage"
	"loomtale/api/internal/tenant"
)

// PipelineAPI implements the jobs/runs/steps/gpu/events slice of
// gen.StrictServerInterface. Named distinctly (not "Handler") so the
// composite server in cmd/api can embed every domain handler without a
// field-name collision.
type PipelineAPI struct {
	Engine    *pipeline.Engine
	Storage   *storage.Internal
	Hub       *sse.Hub
	Probe     pipeline.GpuProbe       // nil until phase 4 wires a real one
	Residency pipeline.ModelResidency // NoopResidency when WORKER_GPU=false

	// ShutdownSignal is cancelled (via http.Server.RegisterOnShutdown, in
	// cmd/api/main.go) when the process starts a graceful shutdown. A
	// plain http.Server.Shutdown never cancels an in-flight request's own
	// context — by design, so a normal handler is never yanked out from
	// under a client mid-response — but an SSE stream has no natural end
	// of its own, so without this every open /events connection would
	// keep Shutdown waiting the full ShutdownTimeout on every deploy.
	// StreamEvents derives each subscription's context from both this and
	// the request's own context, so either one ending closes the stream.
	ShutdownSignal context.Context
}

func toRunDTO(r dbgen.PipelineRun) gen.PipelineRun {
	dto := gen.PipelineRun{
		Id:        idconv.FromPg(r.ID),
		ScopeKind: r.ScopeKind,
		ScopeId:   idconv.FromPg(r.ScopeID),
		Kind:      r.Kind,
		Status:    gen.PipelineRunStatus(r.Status),
		CreatedAt: idconv.FromPgTimestamptz(r.CreatedAt),
	}
	if r.SupersededBy.Valid {
		id := idconv.FromPg(r.SupersededBy)
		dto.SupersededBy = &id
	}
	return dto
}

// toStepDTO converts a step row to its wire DTO. includeErrorMsg gates
// error_msg (a handler's own free-text error, scrubbed before storage
// but never intended for anyone below editor: the log it stands next to
// is already editor-only) — error_code alone is always included, since
// it is a fixed, non-sensitive classification string. Every caller in
// this package sees the same step rows either way; only what gets
// serialized differs by the caller's own role.
func toStepDTO(s dbgen.PipelineStep, includeErrorMsg bool) gen.PipelineStep {
	dto := gen.PipelineStep{
		Id:            idconv.FromPg(s.ID),
		RunId:         idconv.FromPg(s.RunID),
		ScopeKind:     s.ScopeKind,
		ScopeId:       idconv.FromPg(s.ScopeID),
		Kind:          s.Kind,
		Queue:         gen.PipelineStepQueue(s.Queue),
		Priority:      int(s.Priority),
		Status:        gen.PipelineStepStatus(s.Status),
		Attempt:       int(s.Attempt),
		Version:       s.Version,
		RemainingDeps: int(s.RemainingDeps),
		Progress:      int(s.Progress),
		CreatedAt:     idconv.FromPgTimestamptz(s.CreatedAt),
	}
	if s.ProviderRef != "" {
		dto.ProviderRef = &s.ProviderRef
	}
	if s.EtaS.Valid {
		eta := int(s.EtaS.Int32)
		dto.EtaS = &eta
	}
	if s.ErrorCode.Valid {
		dto.ErrorCode = &s.ErrorCode.String
	}
	if includeErrorMsg && s.ErrorMsg.Valid {
		dto.ErrorMsg = &s.ErrorMsg.String
	}
	if s.LogAssetID.Valid {
		id := idconv.FromPg(s.LogAssetID)
		dto.LogAssetId = &id
	}
	if s.StartedAt.Valid {
		t := idconv.FromPgTimestamptz(s.StartedAt)
		dto.StartedAt = &t
	}
	if s.FinishedAt.Valid {
		t := idconv.FromPgTimestamptz(s.FinishedAt)
		dto.FinishedAt = &t
	}
	return dto
}

func toStepSummary(s dbgen.PipelineStep) gen.StepSummary {
	dto := gen.StepSummary{
		Id:       idconv.FromPg(s.ID),
		Kind:     s.Kind,
		Status:   gen.StepSummaryStatus(s.Status),
		Priority: int(s.Priority),
		Progress: int(s.Progress),
	}
	if s.EtaS.Valid {
		eta := int(s.EtaS.Int32)
		dto.EtaS = &eta
	}
	return dto
}

// nowUTC exists purely to keep time.Now().UTC() out of every call site
// that needs a deterministic-looking timestamp field.
func nowUTC() time.Time { return time.Now().UTC() }

// isEditorOrAbove reports whether the resolved tenant role in ctx is
// "editor" or "owner", used to gate a step's error_msg (see toStepDTO):
// a viewer-role route (ListJobs, ListRunSteps) never includes it, since
// the same free-text error a step's editor-only log sits next to should
// not be more widely visible than that log is.
func isEditorOrAbove(ctx context.Context) bool {
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return false
	}
	return info.Role == "editor" || info.Role == "owner"
}
