// Package pipelineapi implements the jobs/runs/steps/gpu slice of the
// generated strict server interface: everything phase 3's HTTP surface
// exposes on top of api/internal/pipeline.Engine. GET /events is not
// here (see api/internal/sse.Handler): it is mounted directly on the
// chi router instead of through the generated strict interface.
package pipelineapi

import (
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/sse"
	"loomtale/api/internal/storage"
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

func toStepDTO(s dbgen.PipelineStep) gen.PipelineStep {
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
	if s.ErrorMsg.Valid {
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

func toModelRefSpecs(specs []gen.CreateStepSpec) []pipeline.StepSpec {
	out := make([]pipeline.StepSpec, len(specs))
	for i, s := range specs {
		var dependsOn []uuid.UUID
		if s.DependsOn != nil {
			dependsOn = make([]uuid.UUID, len(*s.DependsOn))
			copy(dependsOn, *s.DependsOn)
		}
		out[i] = pipeline.StepSpec{
			ID:        s.Id,
			Kind:      s.Kind,
			ScopeKind: s.ScopeKind,
			ScopeID:   s.ScopeId,
			Priority:  s.Priority,
			DependsOn: dependsOn,
		}
	}
	return out
}

// nowUTC exists purely to keep time.Now().UTC() out of every call site
// that needs a deterministic-looking timestamp field.
func nowUTC() time.Time { return time.Now().UTC() }
