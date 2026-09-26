// Package modelsapi implements the /models slice of the generated strict
// server interface: the Model manager's list, install, pause, load and
// unload actions. Installs, loads and unloads are pipeline steps run by
// the worker (the only process with the models volume and the GPU
// network); this package only validates, records and enqueues them.
package modelsapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
	"loomtale/api/internal/models"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/workerstatus"
	"loomtale/api/internal/tenant"
)

// ModelsAPI implements the models slice of gen.StrictServerInterface.
type ModelsAPI struct {
	Manifest     *models.Manifest
	Store        *models.Store
	Queries      *dbgen.Queries
	Engine       *pipeline.Engine
	WorkerStatus workerStatusReader
}

type workerStatusReader interface {
	Get(ctx context.Context) (workerstatus.Status, error)
}

// ListModels implements gen.StrictServerInterface.
func (h *ModelsAPI) ListModels(ctx context.Context, _ gen.ListModelsRequestObject) (gen.ListModelsResponseObject, error) {
	installs, err := h.installsByName(ctx)
	if err != nil {
		return nil, err
	}
	ws := h.workerStatus(ctx)

	out := gen.ModelList{Items: make([]gen.ModelInfo, 0, len(h.Manifest.Models))}
	if ws != nil {
		online := ws.Fresh
		out.WorkerOnline = &online
		if ws.Fresh && ws.GPU.BudgetMB > 0 {
			budget := ws.GPU.BudgetMB
			out.BudgetMb = &budget
		}
	}
	for _, e := range h.Manifest.Models {
		row, has := installs[e.Name]
		out.Items = append(out.Items, toModelInfo(e, row, has, ws))
	}
	return gen.ListModels200JSONResponse(out), nil
}

// InstallModel implements gen.StrictServerInterface.
func (h *ModelsAPI) InstallModel(ctx context.Context, req gen.InstallModelRequestObject) (gen.InstallModelResponseObject, error) {
	e, ok := h.Manifest.Get(req.Name)
	if !ok {
		return gen.InstallModel404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "unknown model", "")), nil
	}
	if err := models.Gate(e); err != nil {
		return gen.InstallModel422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "licence not allowed", err.Error())), nil
	}
	if row, err := h.Queries.GetModelInstall(ctx, e.Name); err == nil && row.Status == models.StatusInstalled {
		return gen.InstallModel409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "model already installed", "")), nil
	}

	info := tenant.MustFromCtx(ctx)
	verified, err := h.Store.VerifiedBytes(ctx, e)
	if err != nil {
		return nil, err
	}
	row, err := h.Store.Claim(ctx, e, info.ID, verified)
	if errors.Is(err, models.ErrAlreadyDownloading) {
		return gen.InstallModel409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "download already running", "")), nil
	}
	if err != nil {
		return nil, err
	}

	runID, stepID, err := h.enqueue(ctx, "models.install", models.KindPull, models.ScopeID(e.Name), pipeline.PriorityTrainBench)
	if err != nil {
		failCtx := context.WithoutCancel(ctx)
		_ = h.Queries.MarkModelInstallFailed(failCtx, dbgen.MarkModelInstallFailedParams{Name: e.Name, Error: idconv.ToPgText("could not queue the download")})
		return nil, err
	}
	if err := h.Queries.SetModelInstallStep(ctx, dbgen.SetModelInstallStepParams{Name: e.Name, RunID: idconv.ToPg(runID), StepID: idconv.ToPg(stepID)}); err != nil {
		return nil, err
	}
	h.audit(ctx, "model.install", e.Name)

	row.RunID, row.StepID = idconv.ToPg(runID), idconv.ToPg(stepID)
	return gen.InstallModel202JSONResponse(toModelInfo(e, row, true, h.workerStatus(ctx))), nil
}

// PauseModelInstall implements gen.StrictServerInterface.
func (h *ModelsAPI) PauseModelInstall(ctx context.Context, req gen.PauseModelInstallRequestObject) (gen.PauseModelInstallResponseObject, error) {
	e, ok := h.Manifest.Get(req.Name)
	if !ok {
		return gen.PauseModelInstall404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "unknown model", "")), nil
	}
	row, err := h.Queries.PauseModelInstall(ctx, e.Name)
	if err != nil {
		return gen.PauseModelInstall409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "no download is running", "")), nil
	}
	// The pull step runs under the tenant that started it; cancelling it
	// makes the worker's heartbeat stop the download, keeping its
	// partial file for the next install to resume.
	if row.StepID.Valid && row.StartedByTenantID.Valid {
		_, cancelErr := h.Engine.CancelStep(ctx, idconv.FromPg(row.StartedByTenantID), idconv.FromPg(row.StepID))
		if cancelErr != nil && !errors.Is(cancelErr, pipeline.ErrNotFound) {
			return nil, cancelErr
		}
	}
	h.audit(ctx, "model.pause", e.Name)
	return gen.PauseModelInstall200JSONResponse(toModelInfo(e, row, true, h.workerStatus(ctx))), nil
}

// LoadModel implements gen.StrictServerInterface.
func (h *ModelsAPI) LoadModel(ctx context.Context, req gen.LoadModelRequestObject) (gen.LoadModelResponseObject, error) {
	e, ok := h.Manifest.Get(req.Name)
	if !ok {
		return gen.LoadModel404ApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "unknown model", "")), nil
	}
	if err := models.Gate(e); err != nil {
		return gen.LoadModel422ApplicationProblemPlusJSONResponse(problem(http.StatusUnprocessableEntity, "licence not allowed", err.Error())), nil
	}
	row, err := h.Queries.GetModelInstall(ctx, e.Name)
	if err != nil || row.Status != models.StatusInstalled {
		return gen.LoadModel409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "model is not installed", "")), nil
	}
	runID, stepID, err := h.enqueue(ctx, "models.load", models.KindLoad, models.ScopeID(e.Name), pipeline.PriorityInteractive)
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "model.load", e.Name)
	return gen.LoadModel202JSONResponse(gen.ModelActionResult{RunId: runID, StepId: stepID}), nil
}

// UnloadModels implements gen.StrictServerInterface.
func (h *ModelsAPI) UnloadModels(ctx context.Context, _ gen.UnloadModelsRequestObject) (gen.UnloadModelsResponseObject, error) {
	runID, stepID, err := h.enqueue(ctx, "models.unload", models.KindUnload, models.UnloadScopeID, pipeline.PriorityInteractive)
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "model.unload", "gpu")
	return gen.UnloadModels202JSONResponse(gen.ModelActionResult{RunId: runID, StepId: stepID}), nil
}

// enqueue creates a one-step run in the caller's tenant.
func (h *ModelsAPI) enqueue(ctx context.Context, runKind, stepKind string, scopeID uuid.UUID, priority int) (uuid.UUID, uuid.UUID, error) {
	info := tenant.MustFromCtx(ctx)
	runID, stepID := idconv.NewV7(), idconv.NewV7()
	spec := pipeline.RunSpec{
		ID: runID, ScopeKind: models.ScopeKind, ScopeID: scopeID, Kind: runKind,
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: stepKind, ScopeKind: models.ScopeKind, ScopeID: scopeID, Priority: priority}},
	}
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		spec.CreatedBy = &sess.UserID
	}
	if _, err := h.Engine.Enqueue(ctx, info.ID, spec); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return runID, stepID, nil
}

func (h *ModelsAPI) installsByName(ctx context.Context) (map[string]dbgen.ModelInstall, error) {
	rows, err := h.Queries.ListModelInstalls(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]dbgen.ModelInstall, len(rows))
	for _, r := range rows {
		out[r.Name] = r
	}
	return out, nil
}

func (h *ModelsAPI) workerStatus(ctx context.Context) *workerstatus.Status {
	if h.WorkerStatus == nil {
		return nil
	}
	ws, err := h.WorkerStatus.Get(ctx)
	if err != nil {
		return nil
	}
	return &ws
}

// toModelInfo merges a manifest entry with its install row (if any) and
// the worker's residency report.
func toModelInfo(e models.Entry, row dbgen.ModelInstall, has bool, ws *workerstatus.Status) gen.ModelInfo {
	allowed := models.LicenceAllowed(e.Licence.SPDX)
	info := gen.ModelInfo{
		Name:       e.Name,
		Task:       e.Task,
		Title:      e.Title,
		Engine:     e.Engine,
		Licence:    gen.ModelLicence{Spdx: e.Licence.SPDX, Url: e.Licence.URL, Verified: e.Licence.Verified, Allowed: allowed},
		SizeBytes:  e.SizeBytes(),
		VramMb:     e.VRAMMB,
		Status:     gen.ModelInfoStatus(models.StatusNotInstalled),
		BytesTotal: e.SizeBytes(),
	}
	switch {
	case !allowed:
		info.Status = gen.ModelInfoStatus(models.StatusBlocked)
	case has:
		info.Status = gen.ModelInfoStatus(row.Status)
		info.BytesDone = row.BytesDone
		if row.BytesTotal > 0 {
			info.BytesTotal = row.BytesTotal
		}
		if row.Error.Valid {
			msg := row.Error.String
			info.Error = &msg
		}
		if row.InstalledAt.Valid {
			t := row.InstalledAt.Time
			info.InstalledAt = &t
		}
	}
	if ws != nil && ws.Fresh {
		info.Loaded = ws.ResidentRef == e.Engine+":"+e.Name
		info.OverBudget = ws.GPU.BudgetMB > 0 && e.VRAMMB > ws.GPU.BudgetMB
	}
	return info
}

func problem(status int, title, detail string) gen.Problem {
	p := gen.Problem{Status: status, Title: title}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// audit records a model action. Best effort, like every other audit
// write in the API: a failure is logged and never fails the request.
func (h *ModelsAPI) audit(ctx context.Context, action, target string) {
	info, err := tenant.FromCtx(ctx)
	if err != nil {
		return
	}
	entry := audit.Entry{TenantID: &info.ID, Action: action, TargetType: "model", TargetID: target}
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		entry.ActorUserID = &sess.UserID
	}
	if r := httpx.RequestFromCtx(ctx); r != nil {
		entry.RemoteAddr = r.RemoteAddr
		entry.UserAgent = r.UserAgent()
	}
	if err := audit.Record(ctx, h.Queries, entry); err != nil {
		slog.ErrorContext(ctx, "modelsapi: failed to write audit entry", "action", action, "error", err)
	}
}
