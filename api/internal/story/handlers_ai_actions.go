package story

import (
	"context"
	"encoding/json"
	"net/http"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// AiActionInput is the step's Input payload (pipeline_steps.input): the
// parts of AiActionRequest the async handler needs beyond its own
// scope_kind/scope_id. Instruction is re-capped by storyctx.Build
// regardless, so a caller that already validated the 500-char OpenAPI
// limit gets no surprise truncation, only defense in depth.
//
// Action is persisted (not just used to pick the pipeline step kind) so
// runEpisodeAction can tell rewrite/expand/shorten/tone apart even though
// they share KindRewrite, and so the apply-step endpoint later reads back
// the right replace-vs-insert semantics for the step it is applying.
//
// SourceLang/TargetLang are set only for llm.translate (CommitImport is
// the only caller today); AutoApply marks a translate step whose result
// should be written straight into the target draft rather than left for a
// human to accept as a proposal (see runTranslate).
type AiActionInput struct {
	Action       string   `json:"action,omitempty"`
	Lang         string   `json:"lang"`
	ParagraphIds []string `json:"paragraphIds,omitempty"`
	BeatId       string   `json:"beatId,omitempty"`
	Instruction  string   `json:"instruction,omitempty"`
	SourceLang   string   `json:"sourceLang,omitempty"`
	TargetLang   string   `json:"targetLang,omitempty"`
	AutoApply    bool     `json:"autoApply,omitempty"`
}

// actionToKind maps the client-facing AiActionRequest.Action enum to a
// pipeline step kind. "outline" at episode scope is not supported today
// (llm.outline always creates a new episode under a series scope, per
// POST /series/{id}/generate and the AI action step registration in this
// package's kinds.go); a caller that requests it gets a 404.
var actionToKind = map[gen.AiActionRequestAction]string{
	gen.Continue:   KindContinue,
	gen.ExpandBeat: KindExpandBeat,
	gen.Expand:     KindRewrite,
	gen.Shorten:    KindRewrite,
	gen.Tone:       KindRewrite,
	gen.Rewrite:    KindRewrite,
	gen.Translate:  KindTranslate,
	gen.Summarise:  KindSummarise,
}

// otherTargetLanguage returns the writer language a translation of lang
// goes into: English and Vietnamese translate into each other.
func otherTargetLanguage(lang string) string {
	if lang == "en" {
		return "vi"
	}
	return "en"
}

// isAiActionKind reports whether kind is one CreateAiAction can enqueue.
func isAiActionKind(kind string) bool {
	for _, k := range actionToKind {
		if k == kind {
			return true
		}
	}
	return false
}

// CreateAiAction implements gen.StrictServerInterface: enqueues one
// pipeline step for the requested action, scoped to this episode. Editor
// role is enforced by x-min-role; ownership is re-checked here via a
// tenant-scoped episode lookup. The request's lang/paragraphIds/beatId/
// instruction are carried to the async Run via StepSpec.Input (see
// AiActionInput) and applied in ai_actions.go's runEpisodeAction.
func (h *StoryAPI) CreateAiAction(ctx context.Context, req gen.CreateAiActionRequestObject) (gen.CreateAiActionResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, _ := authpkg.FromCtx(ctx)
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.CreateAiAction404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	kind, ok := actionToKind[req.Body.Action]
	if !ok {
		detail := "this action is not supported at episode scope"
		return gen.CreateAiAction404ApplicationProblemPlusJSONResponse{Title: "unsupported action", Status: http.StatusNotFound, Detail: &detail}, nil
	}

	input := AiActionInput{Action: string(req.Body.Action), Lang: string(req.Body.Lang)}
	if req.Body.Action == gen.Translate {
		// The writer translates the open draft into the other language.
		input.SourceLang = string(req.Body.Lang)
		input.TargetLang = otherTargetLanguage(string(req.Body.Lang))
	}
	if req.Body.BeatId != nil {
		input.BeatId = *req.Body.BeatId
	}
	if req.Body.Instruction != nil {
		input.Instruction = *req.Body.Instruction
	}
	if req.Body.ParagraphIds != nil {
		input.ParagraphIds = *req.Body.ParagraphIds
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	runID := idconv.NewV7()
	stepID := idconv.NewV7()
	if _, err := h.Engine.Enqueue(ctx, info.ID, pipeline.RunSpec{
		ID:        runID,
		ScopeKind: ScopeEpisode,
		ScopeID:   req.Id,
		Kind:      "episode.ai_action",
		CreatedBy: userIDPtr(sess),
		Steps: []pipeline.StepSpec{{
			ID:        stepID,
			Kind:      kind,
			ScopeKind: ScopeEpisode,
			ScopeID:   req.Id,
			Priority:  pipeline.PriorityInteractive,
			Input:     inputJSON,
		}},
	}); err != nil {
		return nil, err
	}

	return gen.CreateAiAction202JSONResponse{RunId: runID, StepId: stepID}, nil
}

// aiActionOutput mirrors what ai_actions.go's runEpisodeAction stores as
// its step Output (pipeline.Output is a map[string]any, decoded here
// into a typed shape).
type aiActionOutput struct {
	Provider string `json:"provider"`
	Lang     string `json:"lang"`
	Text     string `json:"text"`
	Tainted  bool   `json:"tainted"`
}

// GetAiActionResult implements gen.StrictServerInterface: the client
// polls this (or refetches on the step's SSE transition-to-terminal
// event) rather than receiving incremental token deltas, since no
// per-token text-streaming write path exists yet (see ai_actions.go's
// deltaFlushInterval doc comment).
func (h *StoryAPI) GetAiActionResult(ctx context.Context, req gen.GetAiActionResultRequestObject) (gen.GetAiActionResultResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, err := h.requireEpisode(ctx, info.ID, req.Id); err != nil {
		if isNoRows(err) {
			return gen.GetAiActionResult404ApplicationProblemPlusJSONResponse{Title: "episode not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}

	step, err := h.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(req.StepId)})
	if err != nil {
		if isNoRows(err) {
			return gen.GetAiActionResult404ApplicationProblemPlusJSONResponse{Title: "step not found", Status: http.StatusNotFound}, nil
		}
		return nil, err
	}
	// The step must be an AI action on this very episode: a tenant-scoped
	// lookup alone would let any step id (another episode's, or a non-AI
	// step's output) be read through this episode's URL.
	if step.ScopeKind != ScopeEpisode || idconv.FromPg(step.ScopeID) != req.Id || !isAiActionKind(step.Kind) {
		return gen.GetAiActionResult404ApplicationProblemPlusJSONResponse{Title: "step not found", Status: http.StatusNotFound}, nil
	}

	result := gen.AiActionResult{Status: gen.AiActionResultStatus(step.Status)}
	if step.ErrorMsg.Valid && step.ErrorMsg.String != "" {
		detail := step.ErrorMsg.String
		result.ErrorDetail = &detail
	}
	if step.Status == "done" && len(step.Output) > 0 {
		var out aiActionOutput
		if err := json.Unmarshal(step.Output, &out); err == nil && out.Text != "" {
			result.Provider = &out.Provider
			lang := gen.TargetLanguage(out.Lang)
			result.Lang = &lang
			result.Text = &out.Text
			tainted := out.Tainted
			result.Tainted = &tainted
		}
	}
	return gen.GetAiActionResult200JSONResponse(result), nil
}
