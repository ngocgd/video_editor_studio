package story

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/llm"
	"loomtale/api/internal/providers/registry"
	"loomtale/api/internal/storyctx"
)

// deltaFlushInterval is how often streamed tokens are folded into a
// progress update while a step is running. Real per-token text streaming
// into the step's output mid-run (llm.delta events with actual content,
// not just a numeric percentage) needs a DB write path this phase does
// not add (StepContext.Progress is numeric-only); this handler batches
// deltas at this interval into a numeric liveness Progress() instead,
// and returns the full text as part of the step's final Output once the
// call completes. A client polls/subscribes for the step's terminal
// event and reads Output.text, rather than receiving incremental text.
const deltaFlushInterval = 100 * time.Millisecond

// AIActionHandler is a generic pipeline.StepHandler for every llm.* step
// kind: it resolves a provider via registry.Resolve, builds the
// nonce-fenced storyctx.Context, calls Stream/Generate, and stores a
// structured result. One instance is registered per kind (see Register).
type AIActionHandler struct {
	kind      string
	action    registry.Action
	templateKey string
	schema    string // non-empty for schema-constrained outputs (outline, bible_seed)

	Registry *registry.Registry
	Queries  *dbgen.Queries
}

var _ pipeline.StepHandler = (*AIActionHandler)(nil)

// Handlers returns one AIActionHandler per llm.* step kind, ready to
// Register into a pipeline.Registry.
func Handlers(reg *registry.Registry, queries *dbgen.Queries) []*AIActionHandler {
	specs := []struct {
		kind, templateKey string
		action            registry.Action
		schema            string
	}{
		{KindOutline, "outline", registry.ActionOutline, outlineSchema},
		{KindBibleSeed, "bible_seed", registry.ActionOutline, bibleSeedSchema},
		{KindExpandBeat, "expand_beat", registry.ActionDraft, ""},
		{KindContinue, "continue", registry.ActionDraft, ""},
		{KindRewrite, "rewrite", registry.ActionRewrite, ""},
		{KindTranslate, "translate", registry.ActionTranslate, ""},
		{KindSummarise, "summarise", registry.ActionSummary, ""},
	}
	out := make([]*AIActionHandler, 0, len(specs))
	for _, s := range specs {
		out = append(out, &AIActionHandler{kind: s.kind, action: s.action, templateKey: s.templateKey, schema: s.schema, Registry: reg, Queries: queries})
	}
	return out
}

func (h *AIActionHandler) Kind() string { return h.kind }

func (h *AIActionHandler) Queue(ctx context.Context, s pipeline.StepRef) (string, error) {
	return h.Registry.QueueFor(ctx, s.TenantID, h.action)
}

// InputHash hashes the scope+kind+provider so a re-run against unchanged
// upstream content (bible/draft) is recognized as stale only when the
// resolved provider or scope identity actually changes; content-level
// staleness detection (e.g. bible edited since last outline) is left to
// a future phase per the "reviewable diff, not silent regen" contract.
func (h *AIActionHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	_, providerName, _ := h.Registry.Resolve(ctx, s.TenantID, h.action)
	return fmt.Sprintf("%s:%s:%s:%s", h.kind, s.ScopeKind, s.ScopeID, providerName), nil
}

// ModelRef reports GPU residency only when the resolved provider is
// Ollama.
func (h *AIActionHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	_, providerName, err := h.Registry.Resolve(ctx, s.TenantID, h.action)
	if err != nil {
		return nil, nil // unresolved provider: Queue/Run will surface the real error
	}
	return h.Registry.ModelRefFor(providerName), nil
}

func (h *AIActionHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	tenantID := sc.Tenant()
	provider, providerName, err := h.Registry.Resolve(ctx, tenantID, h.action)
	if err != nil {
		return nil, err
	}

	switch h.kind {
	case KindBibleSeed:
		return h.runBibleSeed(ctx, sc, tenantID, provider, providerName)
	case KindOutline:
		return h.runOutline(ctx, sc, tenantID, provider, providerName)
	default:
		return h.runEpisodeAction(ctx, sc, tenantID, provider, providerName)
	}
}

func (h *AIActionHandler) runBibleSeed(ctx context.Context, sc *pipeline.StepContext, tenantID uuid.UUID, provider llm.Provider, providerName string) (pipeline.Output, error) {
	seriesID := sc.ScopeID()
	series, err := h.Queries.GetSeriesByID(ctx, dbgen.GetSeriesByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(seriesID)})
	if err != nil {
		return nil, err
	}

	settings := seriesSettingsSummary(series)
	built := storyctx.Build(h.templateKey, storyctx.BuildRequest{
		Target:      storyctx.TaintedContent{Text: settings, Origin: llm.OriginUser},
		TokenBudget: defaultTokenBudget,
	})

	resp, err := llm.GenerateStructured(ctx, llm.Request{System: built.System, Data: built.Data, JSONSchema: h.schema, MaxTokens: 4000}, provider.Generate)
	if err != nil {
		return nil, err
	}

	var raw map[string]string
	if err := json.Unmarshal([]byte(resp.Text), &raw); err != nil {
		return nil, fmt.Errorf("story: parse bible_seed response: %w", err)
	}

	// One atomic update per section, so an edit someone saves meanwhile to
	// another section survives, and sections a person wrote are kept.
	for name, content := range raw {
		if err := h.Queries.SeedStoryBibleSection(ctx, dbgen.SeedStoryBibleSectionParams{
			Section: name, Content: content, TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID),
		}); err != nil {
			return nil, err
		}
	}

	sc.Progress(100, 0)
	return pipeline.Output{"provider": providerName, "sections": mapKeys(raw)}, nil
}

func (h *AIActionHandler) runOutline(ctx context.Context, sc *pipeline.StepContext, tenantID uuid.UUID, provider llm.Provider, providerName string) (pipeline.Output, error) {
	seriesID := sc.ScopeID()
	bibleExcerpt, bibleTainted, err := h.loadBibleExcerpt(ctx, tenantID, seriesID)
	if err != nil {
		return nil, err
	}
	idx, err := h.Queries.NextEpisodeIdx(ctx, dbgen.NextEpisodeIdxParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return nil, err
	}

	built := storyctx.Build(h.templateKey, storyctx.BuildRequest{
		BibleExcerpt: storyctx.TaintedContent{Text: bibleExcerpt, Origin: originOf(bibleTainted), Tainted: bibleTainted},
		Target:       storyctx.TaintedContent{Text: fmt.Sprintf("Write the ordered beat outline for episode %d of this series.", idx), Origin: llm.OriginUser},
		TokenBudget:  defaultTokenBudget,
	})

	resp, err := llm.GenerateStructured(ctx, llm.Request{System: built.System, Data: built.Data, JSONSchema: h.schema, MaxTokens: 4000}, provider.Generate)
	if err != nil {
		return nil, err
	}
	var beats []outlineBeatDoc
	if err := json.Unmarshal([]byte(resp.Text), &beats); err != nil {
		return nil, fmt.Errorf("story: parse outline response: %w", err)
	}
	outlineJSON, err := json.Marshal(beats)
	if err != nil {
		return nil, err
	}

	episode, err := h.Queries.CreateEpisode(ctx, dbgen.CreateEpisodeParams{
		ID:       idconv.ToPg(idconv.NewV7()),
		TenantID: idconv.ToPg(tenantID),
		SeriesID: idconv.ToPg(seriesID),
		Idx:      idx,
		Title:    fmt.Sprintf("Episode %d", idx),
		Outline:  outlineJSON,
		Status:   "outlined",
	})
	if err != nil {
		return nil, err
	}

	sc.Progress(100, 0)
	return pipeline.Output{"provider": providerName, "episodeId": idconv.FromPg(episode.ID).String(), "beatCount": len(beats)}, nil
}

// runEpisodeAction handles expand_beat/continue/rewrite/translate/summarise.
// It reads the request's lang/paragraphIds/beatId/instruction back from
// the step's Input (set at enqueue time by CreateAiAction), so an action
// targets exactly the paragraphs the user selected, with their own
// instruction applied, rather than the episode's whole draft.
func (h *AIActionHandler) runEpisodeAction(ctx context.Context, sc *pipeline.StepContext, tenantID uuid.UUID, provider llm.Provider, providerName string) (pipeline.Output, error) {
	var in AiActionInput
	if err := sc.Input(&in); err != nil {
		return nil, fmt.Errorf("story: decode ai action input: %w", err)
	}

	episodeID := sc.ScopeID()
	episode, err := h.Queries.GetEpisodeByID(ctx, dbgen.GetEpisodeByIDParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(episodeID)})
	if err != nil {
		return nil, err
	}
	lang, draft, err := h.pickDraft(ctx, tenantID, episodeID, in.Lang)
	if err != nil {
		return nil, err
	}
	paragraphs, err := decodeParagraphs(draft.Paragraphs)
	if err != nil {
		return nil, err
	}
	selected := selectParagraphs(paragraphs, in.ParagraphIds)
	targetText := joinParagraphs(selected)
	targetTainted := anyParagraphTainted(selected)

	bibleExcerpt, bibleTainted, err := h.loadBibleExcerpt(ctx, tenantID, idconv.FromPg(episode.SeriesID))
	if err != nil {
		bibleExcerpt, bibleTainted = "", false // no bible yet for this series: proceed without it rather than failing the action
	}

	built := storyctx.Build(h.templateKey, storyctx.BuildRequest{
		BibleExcerpt: storyctx.TaintedContent{Text: bibleExcerpt, Origin: originOf(bibleTainted), Tainted: bibleTainted},
		Previously:   storyctx.TaintedContent{Text: draft.Summary, Origin: originOf(draft.SummaryTainted), Tainted: draft.SummaryTainted},
		Target:       storyctx.TaintedContent{Text: targetText, Origin: originOf(targetTainted), Tainted: targetTainted},
		Instruction:  in.Instruction,
		TokenBudget:  defaultTokenBudget,
	})

	var mu sync.Mutex
	var buffer strings.Builder
	lastFlush := time.Now()
	onDelta := func(d llm.Delta) {
		mu.Lock()
		buffer.WriteString(d.Text)
		flush := time.Since(lastFlush) >= deltaFlushInterval
		if flush {
			lastFlush = time.Now()
		}
		n := buffer.Len()
		mu.Unlock()
		if flush {
			sc.Progress(progressFromLength(n), 0)
		}
	}

	resp, err := provider.Stream(ctx, llm.Request{System: built.System, Data: built.Data, MaxTokens: 4000}, onDelta)
	if err != nil {
		return nil, err
	}

	resultTainted := built.Tainted
	if h.kind == KindSummarise {
		if _, err := h.Queries.UpdateDraftSummary(ctx, dbgen.UpdateDraftSummaryParams{
			Summary: resp.Text, SummaryTainted: resultTainted, TenantID: idconv.ToPg(tenantID), ID: draft.ID,
		}); err != nil {
			return nil, err
		}
	}

	sc.Progress(100, 0)
	return pipeline.Output{
		"provider": providerName,
		"lang":     lang,
		"text":     resp.Text,
		"tainted":  resultTainted,
	}, nil
}

// pickDraft returns the draft for preferredLang if set and it exists,
// else the episode's "en" draft if present, else its only draft, else an
// error (no draft exists yet to act on).
func (h *AIActionHandler) pickDraft(ctx context.Context, tenantID, episodeID uuid.UUID, preferredLang string) (string, dbgen.EpisodeDraft, error) {
	tryLangs := []string{"en", "vi"}
	if preferredLang == "en" || preferredLang == "vi" {
		tryLangs = []string{preferredLang}
		for _, l := range []string{"en", "vi"} {
			if l != preferredLang {
				tryLangs = append(tryLangs, l)
			}
		}
	}
	for _, lang := range tryLangs {
		if d, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang}); err == nil {
			return lang, d, nil
		}
	}
	return "", dbgen.EpisodeDraft{}, fmt.Errorf("story: no draft exists for episode %s", episodeID)
}

// selectParagraphs returns the subset of all whose Id is in ids, in
// all's original order, preserving ties; an empty/nil ids selects every
// paragraph (the whole-draft case, e.g. Continue with no selection).
func selectParagraphs(all []Paragraph, ids []string) []Paragraph {
	if len(ids) == 0 {
		return all
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]Paragraph, 0, len(ids))
	for _, p := range all {
		if want[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func (h *AIActionHandler) loadBibleExcerpt(ctx context.Context, tenantID, seriesID uuid.UUID) (string, bool, error) {
	bible, err := h.Queries.GetStoryBible(ctx, dbgen.GetStoryBibleParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
	if err != nil {
		return "", false, err
	}
	sections, err := decodeBibleSections(bible.Sections)
	if err != nil {
		return "", false, err
	}
	var b strings.Builder
	tainted := false
	for _, name := range []string{"style_guide", "arcs", "world", "glossary"} {
		s, ok := sections[name]
		if !ok || s.Content == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s]\n%s\n\n", name, s.Content)
		if s.Tainted {
			tainted = true
		}
	}
	return strings.TrimSpace(b.String()), tainted, nil
}

func seriesSettingsSummary(s dbgen.Series) string {
	return fmt.Sprintf("Title: %s\nGenre: %s\nStyle notes: %s\nPlanned episodes: %d\nTarget episode minutes: %d\nTarget languages: %s",
		s.Title, s.Genre, s.StyleNotes, s.PlannedEpisodeCount, s.TargetEpisodeMinutes, strings.Join(s.TargetLanguages, ","))
}

func joinParagraphs(paragraphs []Paragraph) string {
	texts := make([]string, len(paragraphs))
	for i, p := range paragraphs {
		texts[i] = p.Text
	}
	return strings.Join(texts, "\n\n")
}

func anyParagraphTainted(paragraphs []Paragraph) bool {
	for _, p := range paragraphs {
		if p.Tainted {
			return true
		}
	}
	return false
}

func originOf(tainted bool) llm.DataOrigin {
	if tainted {
		return llm.OriginDerived
	}
	return llm.OriginUser
}

func progressFromLength(chars int) int {
	// A rough, bounded liveness indicator (not a real completion
	// percentage, since total length is unknown ahead of time).
	pct := chars / 40
	if pct > 95 {
		return 95
	}
	if pct < 1 {
		return 1
	}
	return pct
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
