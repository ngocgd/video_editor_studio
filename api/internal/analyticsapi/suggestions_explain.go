package analyticsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"loomtale/api/internal/analytics"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/tenant"
)

// ListAnalyticsSuggestions implements gen.StrictServerInterface.
func (h *AnalyticsAPI) ListAnalyticsSuggestions(ctx context.Context, request gen.ListAnalyticsSuggestionsRequestObject) (gen.ListAnalyticsSuggestionsResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, ok, err := h.channel(ctx, info.ID, request.Id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return gen.ListAnalyticsSuggestions404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	includeDismissed := request.Params.IncludeDismissed != nil && *request.Params.IncludeDismissed
	rows, err := h.Aggregator.Suggestions(ctx, info.ID, request.Id, request.Params.VideoId, includeDismissed)
	if err != nil {
		return nil, err
	}
	items := make([]gen.AnalyticsSuggestion, 0, len(rows))
	for _, s := range rows {
		items = append(items, gen.AnalyticsSuggestion{
			VideoId: s.VideoID, Rule: s.Rule, Version: int(s.Version), Title: s.Title,
			Evidence: s.Evidence, Dismissed: s.Dismissed, UpdatedAt: s.UpdatedAt,
		})
	}
	return gen.ListAnalyticsSuggestions200JSONResponse(gen.AnalyticsSuggestionList{Items: items}), nil
}

// UpdateAnalyticsSuggestion implements gen.StrictServerInterface.
func (h *AnalyticsAPI) UpdateAnalyticsSuggestion(ctx context.Context, request gen.UpdateAnalyticsSuggestionRequestObject) (gen.UpdateAnalyticsSuggestionResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, ok, err := h.channel(ctx, info.ID, request.Id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return gen.UpdateAnalyticsSuggestion404ApplicationProblemPlusJSONResponse(problem(404, "channel not found", "no such channel in this workspace")), nil
	}
	b := request.Body
	err := h.Aggregator.SetDismissed(ctx, info.ID, request.Id, b.VideoId, b.Rule, b.Dismissed)
	if errors.Is(err, analytics.ErrSuggestionNotFound) {
		return gen.UpdateAnalyticsSuggestion404ApplicationProblemPlusJSONResponse(problem(404, "suggestion not found", "no such suggestion for this channel")), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.UpdateAnalyticsSuggestion204Response{}, nil
}

// ExplainAnalyticsChannel implements gen.StrictServerInterface: it
// gathers the channel's aggregates here and queues the Explain step with
// them as its input, so the worker never reads more than those numbers.
func (h *AnalyticsAPI) ExplainAnalyticsChannel(ctx context.Context, request gen.ExplainAnalyticsChannelRequestObject) (gen.ExplainAnalyticsChannelResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	if _, ok, err := h.channel(ctx, info.ID, request.Id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return gen.ExplainAnalyticsChannel404ApplicationProblemPlusJSONResponse{AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse: channelNotFound()}, nil
	}
	in, err := h.Aggregator.ExplainInput(ctx, info.ID, request.Id)
	if errors.Is(err, analytics.ErrNothingToExplain) {
		return gen.ExplainAnalyticsChannel409ApplicationProblemPlusJSONResponse(problem(409, "nothing to explain yet",
			"sync the channel first: it has no analytics data yet")), nil
	}
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	stepID := idconv.NewV7()
	_, err = h.Engine.Enqueue(ctx, info.ID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: analytics.ScopeKindChannel, ScopeID: request.Id,
		Kind: analytics.RunKindExplain, CreatedBy: sessionUser(ctx),
		Steps: []pipeline.StepSpec{{
			ID: stepID, Kind: analytics.KindExplain, ScopeKind: analytics.ScopeKindChannel, ScopeID: request.Id,
			Priority: pipeline.PriorityInteractive, Input: input,
		}},
	})
	if errors.Is(err, pipeline.ErrQuotaExceeded) {
		return gen.ExplainAnalyticsChannel429ApplicationProblemPlusJSONResponse(problem(http.StatusTooManyRequests, "quota exceeded", err.Error())), nil
	}
	if err != nil {
		return nil, fmt.Errorf("analyticsapi: queue explain: %w", err)
	}
	return gen.ExplainAnalyticsChannel202JSONResponse(gen.AnalyticsExplanation{Id: stepID, Status: "queued"}), nil
}

// GetAnalyticsExplanation implements gen.StrictServerInterface. Only
// explain steps are readable here.
func (h *AnalyticsAPI) GetAnalyticsExplanation(ctx context.Context, request gen.GetAnalyticsExplanationRequestObject) (gen.GetAnalyticsExplanationResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	step, err := h.Queries.GetStepByID(ctx, dbgen.GetStepByIDParams{TenantID: idconv.ToPg(info.ID), ID: idconv.ToPg(request.Id)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && step.Kind != analytics.KindExplain) {
		return gen.GetAnalyticsExplanation404ApplicationProblemPlusJSONResponse(problem(404, "explanation not found", "no such explanation in this workspace")), nil
	}
	if err != nil {
		return nil, fmt.Errorf("analyticsapi: read explain step: %w", err)
	}
	out := gen.AnalyticsExplanation{Id: request.Id, Status: gen.AnalyticsExplanationStatus(step.Status)}
	switch step.Status {
	case "done":
		res, err := analytics.DecodeExplainOutput(step.Output)
		if err != nil {
			return nil, fmt.Errorf("analyticsapi: decode explain output: %w", err)
		}
		out.Text, out.Provider, out.CostUsd = &res.Text, &res.Provider, &res.CostUSD
		if res.Model != "" {
			out.Model = &res.Model
		}
	case "failed":
		msg := strings.TrimPrefix(step.ErrorMsg.String, pipeline.ErrValidation.Error()+": ")
		out.Error = &msg
	}
	return gen.GetAnalyticsExplanation200JSONResponse(out), nil
}
