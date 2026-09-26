package scenes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/storyctx"
)

// splitTokenBudget bounds the scene split prompt; a 6k-word draft is
// about 8k tokens.
const splitTokenBudget = 100_000

// splitMaxTokens bounds the reply: a label, a prompt and a few names per
// scene plus one speaker per quoted line.
const splitMaxTokens = 16_000

// SplitInput is the llm.scene_split step input.
type SplitInput struct {
	Lang        string `json:"lang"`
	CadenceMinS int    `json:"cadenceMinS"`
	CadenceMaxS int    `json:"cadenceMaxS"`
	// DiscardWork confirms that the split may delete edited scenes and
	// takes. Without it, work made while the step ran fails the step
	// instead of being deleted.
	DiscardWork bool `json:"discardWork"`
}

// splitSource is a draft and the series' characters, ready to split.
type splitSource struct {
	Episode    dbgen.Episode
	Paragraphs []Paragraph
	Index      NameIndex
	Characters []dbgen.Character
	Settings   Settings
}

func loadSplitSource(ctx context.Context, q *dbgen.Queries, tenantID, episodeID uuid.UUID, lang string) (splitSource, error) {
	tid := idconv.ToPg(tenantID)
	episode, err := q.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: tid, ID: idconv.ToPg(episodeID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return splitSource{}, ErrNotFound
	}
	if err != nil {
		return splitSource{}, err
	}
	draft, err := q.GetDraft(ctx, dbgen.GetDraftParams{TenantID: tid, EpisodeID: episode.ID, Lang: lang})
	if errors.Is(err, pgx.ErrNoRows) {
		return splitSource{}, ErrEmptyDraft
	}
	if err != nil {
		return splitSource{}, err
	}
	var paragraphs []Paragraph
	if err := json.Unmarshal(draft.Paragraphs, &paragraphs); err != nil {
		return splitSource{}, err
	}
	nonEmpty := paragraphs[:0]
	for _, p := range paragraphs {
		if strings.TrimSpace(p.Text) != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return splitSource{}, ErrEmptyDraft
	}
	chars, err := q.ListCharactersBySeries(ctx, dbgen.ListCharactersBySeriesParams{TenantID: tid, SeriesID: episode.SeriesID})
	if err != nil {
		return splitSource{}, err
	}
	settings, err := LoadSettings(ctx, q, tenantID, idconv.FromPg(episode.SeriesID))
	if err != nil {
		return splitSource{}, err
	}
	return splitSource{Episode: episode, Paragraphs: nonEmpty, Index: IndexOf(chars), Characters: chars, Settings: settings}, nil
}

// IndexOf builds the name index of a series' characters.
func IndexOf(chars []dbgen.Character) NameIndex {
	list := make([]CharacterNames, 0, len(chars))
	for _, c := range chars {
		list = append(list, CharacterNames{ID: idconv.FromPg(c.ID), Names: []string{c.NameOrig, c.NameEn, c.NameVi}})
	}
	return NewNameIndex(list)
}

func cadenceOr(c Cadence, minS, maxS int) Cadence {
	if minS > 0 {
		c.MinS = minS
	}
	if maxS > 0 {
		c.MaxS = maxS
	}
	if c.MaxS < c.MinS {
		c.MaxS = c.MinS
	}
	return c
}

// SplitByParagraphsNow is the synchronous paragraph split. Without
// discardWork it refuses (a *DropsWorkError) to delete edited scenes or
// takes.
func (s *Service) SplitByParagraphsNow(ctx context.Context, tenantID, episodeID uuid.UUID, lang string, minS, maxS int, discardWork bool) (SplitResult, error) {
	src, err := loadSplitSource(ctx, s.Queries, tenantID, episodeID, lang)
	if err != nil {
		return SplitResult{}, err
	}
	drafts := SplitByParagraphs(src.Paragraphs, src.Index, cadenceOr(src.Settings.Cadence, minS, maxS), lang)
	return s.ApplySplit(ctx, tenantID, episodeID, lang, drafts, src.Settings.ImageStyleID, discardWork)
}

// EnqueueLLMSplit queues llm.scene_split at interactive priority. The
// drafts are not known until the model answers, so without
// in.DiscardWork it refuses up front when any current scene is edited or
// holds takes, with every scene counted as at risk.
func (s *Service) EnqueueLLMSplit(ctx context.Context, tenantID uuid.UUID, createdBy *uuid.UUID, episodeID uuid.UUID, in SplitInput) (uuid.UUID, uuid.UUID, error) {
	if _, err := loadSplitSource(ctx, s.Queries, tenantID, episodeID, in.Lang); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if !in.DiscardWork {
		rows, err := s.Queries.ListScenesForResplit(ctx, dbgen.ListScenesForResplitParams{
			TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: in.Lang,
		})
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		if risk := RiskOfAll(existingScenes(rows)); risk.LosesWork() {
			return uuid.Nil, uuid.Nil, &DropsWorkError{Risk: risk, UpperBound: true}
		}
	}
	input, err := json.Marshal(in)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	stepID := idconv.NewV7()
	runID, err := s.Engine.Enqueue(ctx, tenantID, pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeEpisode, ScopeID: episodeID, Kind: KindSceneSplit, CreatedBy: createdBy,
		Steps: []pipeline.StepSpec{{ID: stepID, Kind: KindSceneSplit, ScopeKind: ScopeEpisode, ScopeID: episodeID, Priority: pipeline.PriorityInteractive, Input: input}},
	})
	return runID, stepID, err
}

// SplitHandler is llm.scene_split.
type SplitHandler struct{ StepDeps }

var _ pipeline.StepHandler = (*SplitHandler)(nil)

func (h *SplitHandler) Kind() string { return KindSceneSplit }

func (h *SplitHandler) Queue(ctx context.Context, s pipeline.StepRef) (string, error) {
	return h.LLM.QueueFor(ctx, s.TenantID, registry.ActionSceneSplit)
}

func (h *SplitHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	_, provider, _ := h.LLM.Resolve(ctx, s.TenantID, registry.ActionSceneSplit)
	return pipeline.HashInputs(s.Kind, s.ScopeID, provider)
}

func (h *SplitHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	_, provider, err := h.LLM.Resolve(ctx, s.TenantID, registry.ActionSceneSplit)
	if err != nil {
		return nil, nil //nolint:nilerr // Run reports the real error
	}
	return h.LLM.ModelRefFor(provider), nil
}

// rosterText lists the characters by every name they go by, for the
// model to name speakers from.
func rosterText(chars []dbgen.Character) string {
	var b strings.Builder
	for _, c := range chars {
		var names []string
		for _, n := range []string{c.NameEn, c.NameOrig, c.NameVi} {
			if n != "" {
				names = append(names, n)
			}
		}
		fmt.Fprintf(&b, "- %s", strings.Join(names, " / "))
		if c.Role != "" {
			fmt.Fprintf(&b, " (%s)", c.Role)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (h *SplitHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	var in SplitInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("%w: split input: %v", pipeline.ErrValidation, err)
	}
	provider, providerName, err := h.LLM.Resolve(ctx, sc.Tenant(), registry.ActionSceneSplit)
	if err != nil {
		return nil, err
	}
	src, err := loadSplitSource(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID(), in.Lang)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	prompt := buildSplitPrompt(src.Paragraphs)
	tainted := false
	for _, p := range src.Paragraphs {
		tainted = tainted || p.Tainted
	}
	origin := llm.OriginUser
	if tainted {
		origin = llm.OriginImport
	}
	cadence := cadenceOr(src.Settings.Cadence, in.CadenceMinS, in.CadenceMaxS)
	built := storyctx.Build("scene_split", storyctx.BuildRequest{
		Target:      storyctx.TaintedContent{Text: prompt.Text, Origin: origin, Tainted: tainted},
		Characters:  storyctx.TaintedContent{Text: rosterText(src.Characters), Origin: llm.OriginUser},
		Instruction: cadenceInstruction(cadence, in.Lang),
		TokenBudget: splitTokenBudget,
	})
	sc.Progress(5, 0)
	resp, err := llm.GenerateStructured(ctx, llm.Request{System: built.System, Data: built.Data, JSONSchema: sceneSplitSchema, MaxTokens: splitMaxTokens}, provider.Generate)
	if err != nil {
		return nil, err
	}
	var out LLMSplit
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		return nil, fmt.Errorf("scenes: parse scene split reply: %w", err)
	}
	drafts, unrecognised := NormalizeLLMSplit(prompt, out, src.Index, in.Lang)
	res, err := h.Service.ApplySplit(ctx, sc.Tenant(), sc.ScopeID(), in.Lang, drafts, src.Settings.ImageStyleID, in.DiscardWork)
	if errors.Is(err, ErrDropsWork) {
		// Scenes were edited or got takes while the model ran: keep them
		// and let the user split again with a confirmation.
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	if err != nil {
		return nil, err
	}
	sc.Progress(100, 0)
	return pipeline.Output{
		"provider": providerName, "sceneCount": res.Total, "keptCount": res.Kept, "droppedCount": res.Dropped,
		"unrecognisedSpeakers": unrecognised, "tainted": tainted,
	}, nil
}
