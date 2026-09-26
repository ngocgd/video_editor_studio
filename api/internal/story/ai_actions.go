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
	// PinCharacters adds the series' pinned character profiles to every
	// request as their own data block (STORY_PIN_CHARACTERS, default on).
	PinCharacters bool
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
		Characters:   h.pinnedProfiles(ctx, tenantID, seriesID),
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
	// The bible excerpt's taint carries to every beat: cheap to record here
	// (already computed above), and it lets the writer's beats panel show a
	// taint badge before any draft is written from a tainted beat.
	for i := range beats {
		beats[i].Tainted = bibleTainted
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

// defaultRewriteIntent supplies a server-side intent sentence for the
// rewrite-family actions that have no dedicated template (expand, shorten,
// tone all reuse KindRewrite's "rewrite" template) when the caller sent no
// typed instruction of their own. "rewrite" itself has none: without an
// instruction it is just a general polish pass, which the template already
// covers.
var defaultRewriteIntent = map[string]string{
	"expand":  "Expand this text with more sensory detail, interiority and pacing, roughly doubling its length, while preserving its meaning and continuity.",
	"shorten": "Shorten this text to roughly half its length while preserving its key story beats, voice and continuity.",
	"tone":    "Adjust this text's tone to be more vivid and emotionally engaging while preserving its meaning and continuity.",
}

// runEpisodeAction handles expand_beat/continue/rewrite/translate/summarise.
// It reads the request's action/lang/paragraphIds/beatId/instruction back
// from the step's Input (set at enqueue time by CreateAiAction or
// CommitImport), so an action targets exactly the paragraphs the user
// selected (or the beat/language import specified), with their own
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

	if h.kind == KindTranslate {
		return h.runTranslate(ctx, sc, tenantID, episode, provider, providerName, in)
	}

	// Continue and expand_beat write into a draft that may not exist yet
	// (an outlined or manual episode has none until something creates one);
	// every other action needs an existing draft to act on.
	var lang string
	var draft dbgen.EpisodeDraft
	if h.kind == KindContinue || h.kind == KindExpandBeat {
		lang, draft, err = h.pickOrCreateDraft(ctx, tenantID, episodeID, in.Lang)
	} else {
		lang, draft, err = h.pickDraft(ctx, tenantID, episodeID, in.Lang)
	}
	if err != nil {
		return nil, err
	}
	paragraphs, err := decodeParagraphs(draft.Paragraphs)
	if err != nil {
		return nil, err
	}

	var targetText string
	var targetTainted bool
	if h.kind == KindExpandBeat {
		beat, ok := findBeat(episode.Outline, in.BeatId)
		if !ok {
			return nil, fmt.Errorf("story: beat %q not found in episode outline", in.BeatId)
		}
		targetText, targetTainted = beat.Summary, beat.Tainted
	} else {
		selected := selectParagraphs(paragraphs, in.ParagraphIds)
		targetText, targetTainted = joinParagraphs(selected), anyParagraphTainted(selected)
	}

	instruction := in.Instruction
	if h.kind == KindRewrite && instruction == "" {
		instruction = defaultRewriteIntent[in.Action]
	}

	bibleExcerpt, bibleTainted, err := h.loadBibleExcerpt(ctx, tenantID, idconv.FromPg(episode.SeriesID))
	if err != nil {
		bibleExcerpt, bibleTainted = "", false // no bible yet for this series: proceed without it rather than failing the action
	}

	built := storyctx.Build(h.templateKey, storyctx.BuildRequest{
		BibleExcerpt: storyctx.TaintedContent{Text: bibleExcerpt, Origin: originOf(bibleTainted), Tainted: bibleTainted},
		Previously:   storyctx.TaintedContent{Text: draft.Summary, Origin: originOf(draft.SummaryTainted), Tainted: draft.SummaryTainted},
		Target:       storyctx.TaintedContent{Text: targetText, Origin: originOf(targetTainted), Tainted: targetTainted},
		Characters:   h.pinnedProfiles(ctx, tenantID, idconv.FromPg(episode.SeriesID)),
		Instruction:  instruction,
		TokenBudget:  defaultTokenBudget,
	})

	resp, err := h.stream(ctx, sc, provider, built)
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

// runTranslate handles llm.translate. It always reads its own explicit
// SourceLang draft (never the generic pickDraft fallback rewrite/continue
// use) and produces text for TargetLang. When AutoApply is set (an
// import-triggered translate: CommitImport enqueues one per chapter, never
// interactively), the result is written straight into the target draft,
// created if absent, but only when that draft is still empty — an
// interactively-edited target draft is left alone and the translation is
// only returned in the step's output, same as every other action, for the
// writer to review as a proposal.
func (h *AIActionHandler) runTranslate(ctx context.Context, sc *pipeline.StepContext, tenantID uuid.UUID, episode dbgen.Episode, provider llm.Provider, providerName string, in AiActionInput) (pipeline.Output, error) {
	episodeID := idconv.FromPg(episode.ID)
	sourceLang := in.SourceLang
	if sourceLang == "" {
		sourceLang = in.Lang // interactive callers with no distinct source: translate the currently open draft
	}
	targetLang := in.TargetLang
	if targetLang == "" {
		targetLang = in.Lang
	}

	source, err := h.Queries.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: sourceLang})
	if err != nil {
		return nil, err
	}
	paragraphs, err := decodeParagraphs(source.Paragraphs)
	if err != nil {
		return nil, err
	}
	// An interactive translate covers the writer's selection (the whole
	// draft when nothing is selected); an import-triggered one has no
	// selection and translates the whole chapter.
	selected := selectParagraphs(paragraphs, in.ParagraphIds)
	targetText := joinParagraphs(selected)
	targetTainted := anyParagraphTainted(selected)

	bibleExcerpt, bibleTainted, err := h.loadBibleExcerpt(ctx, tenantID, idconv.FromPg(episode.SeriesID))
	if err != nil {
		bibleExcerpt, bibleTainted = "", false
	}

	built := storyctx.Build(h.templateKey, storyctx.BuildRequest{
		BibleExcerpt:       storyctx.TaintedContent{Text: bibleExcerpt, Origin: originOf(bibleTainted), Tainted: bibleTainted},
		Target:             storyctx.TaintedContent{Text: targetText, Origin: originOf(targetTainted), Tainted: targetTainted},
		TargetLanguageName: targetLanguageName(targetLang),
		Characters:         h.pinnedProfiles(ctx, tenantID, idconv.FromPg(episode.SeriesID)),
		TokenBudget:        defaultTokenBudget,
	})

	resp, err := h.stream(ctx, sc, provider, built)
	if err != nil {
		return nil, err
	}
	resultTainted := built.Tainted

	if in.AutoApply {
		if err := h.writeAutoTranslation(ctx, tenantID, episodeID, targetLang, resp.Text, resultTainted); err != nil {
			return nil, err
		}
	}

	sc.Progress(100, 0)
	return pipeline.Output{
		"provider": providerName,
		"lang":     targetLang,
		"text":     resp.Text,
		"tainted":  resultTainted,
	}, nil
}

// writeAutoTranslation stores text as targetLang's draft, creating it if
// absent. If a draft already exists with paragraphs in it (a human, or an
// earlier run, already put content there), the translation is left
// unapplied: the caller's step output still carries the text, so nothing
// is lost, but an editor's own draft is never silently overwritten.
func (h *AIActionHandler) writeAutoTranslation(ctx context.Context, tenantID, episodeID uuid.UUID, lang, text string, tainted bool) error {
	_, draft, err := h.pickOrCreateDraft(ctx, tenantID, episodeID, lang)
	if err != nil {
		return err
	}
	existing, err := decodeParagraphs(draft.Paragraphs)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	paragraphs := paragraphsFromText(text, tainted)
	paragraphsJSON, err := encodeParagraphs(paragraphs)
	if err != nil {
		return err
	}
	_, err = h.Queries.UpdateDraftParagraphs(ctx, dbgen.UpdateDraftParagraphsParams{
		Paragraphs: paragraphsJSON, WordCount: int32(WordCount(paragraphs)), NextVersion: draft.Version + 1,
		TenantID: idconv.ToPg(tenantID), ID: draft.ID, ExpectedVersion: draft.Version,
	})
	if isNoRows(err) {
		return nil // someone else wrote to it in the meantime; leave it alone
	}
	return err
}

// paragraphsFromText splits model output into paragraphs on blank lines,
// each with a fresh id and origin=model.
func paragraphsFromText(text string, tainted bool) []Paragraph {
	blocks := splitBlankLines(text)
	paragraphs := make([]Paragraph, 0, len(blocks))
	for _, b := range blocks {
		paragraphs = append(paragraphs, Paragraph{ID: idconv.NewV7().String(), Text: b, Origin: "model", Tainted: tainted})
	}
	return paragraphs
}

// stream runs provider.Stream with the deltaFlushInterval progress
// heuristic shared by every episode action (interactive rewrite/continue/
// expand_beat and import-triggered translate alike).
func (h *AIActionHandler) stream(ctx context.Context, sc *pipeline.StepContext, provider llm.Provider, built storyctx.Context) (llm.Response, error) {
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
	return provider.Stream(ctx, llm.Request{System: built.System, Data: built.Data, MaxTokens: 4000}, onDelta)
}

// findBeat returns the beat with id beatID from outlineJSON (episodes.
// outline), and whether it was found.
func findBeat(outlineJSON []byte, beatID string) (outlineBeatDoc, bool) {
	beats, err := decodeOutline(outlineJSON)
	if err != nil {
		return outlineBeatDoc{}, false
	}
	for _, b := range beats {
		if b.ID == beatID {
			return b, true
		}
	}
	return outlineBeatDoc{}, false
}

// targetLanguageName returns the human-readable language name the
// translate template names as its target, so the model is told what to
// translate into rather than inferring it from the glossary alone.
func targetLanguageName(lang string) string {
	switch lang {
	case "vi":
		return "Vietnamese"
	case "en":
		return "English"
	default:
		return lang
	}
}

// pickOrCreateDraft returns the lang draft for episodeID, creating an
// empty one if it does not exist yet. Used by continue and expand_beat,
// which must be able to start a fresh episode's first draft, and by
// import-triggered translate's auto-apply path.
func (h *AIActionHandler) pickOrCreateDraft(ctx context.Context, tenantID, episodeID uuid.UUID, lang string) (string, dbgen.EpisodeDraft, error) {
	return getOrCreateDraft(ctx, h.Queries, tenantID, episodeID, lang)
}

// getOrCreateDraft returns the lang draft for episodeID, creating an empty
// one if it does not exist yet; a lang outside the writer's en/vi falls
// back to en.
func getOrCreateDraft(ctx context.Context, q *dbgen.Queries, tenantID, episodeID uuid.UUID, lang string) (string, dbgen.EpisodeDraft, error) {
	if lang != "en" && lang != "vi" {
		lang = "en"
	}
	if d, err := q.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang}); err == nil {
		return lang, d, nil
	} else if !isNoRows(err) {
		return "", dbgen.EpisodeDraft{}, err
	}
	created, err := q.CreateDraftIfAbsent(ctx, dbgen.CreateDraftIfAbsentParams{
		ID: idconv.ToPg(idconv.NewV7()), TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID),
		Lang: lang, Paragraphs: []byte("[]"), WordCount: 0,
	})
	if err == nil {
		return lang, created, nil
	}
	if !isNoRows(err) {
		return "", dbgen.EpisodeDraft{}, err
	}
	// A concurrent creator won the race; read what it wrote.
	d, err := q.GetDraft(ctx, dbgen.GetDraftParams{TenantID: idconv.ToPg(tenantID), EpisodeID: idconv.ToPg(episodeID), Lang: lang})
	return lang, d, err
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

// pinnedProfiles renders the series' pinned character profiles as one
// block ("Name (other names): profile" per line). Profiles are written by
// the workspace's own users, so the block is origin=user and untainted.
// A lookup failure only drops the block: the action still runs.
func (h *AIActionHandler) pinnedProfiles(ctx context.Context, tenantID, seriesID uuid.UUID) storyctx.TaintedContent {
	if !h.PinCharacters {
		return storyctx.TaintedContent{}
	}
	rows, err := h.Queries.ListPinnedCharacterProfiles(ctx, dbgen.ListPinnedCharacterProfilesParams{TenantID: idconv.ToPg(tenantID), SeriesID: idconv.ToPg(seriesID)})
	if err != nil || len(rows) == 0 {
		return storyctx.TaintedContent{}
	}
	var b strings.Builder
	for _, r := range rows {
		var names []string
		for _, n := range []string{r.NameEn, r.NameOrig, r.NameVi} {
			if n != "" {
				names = append(names, n)
			}
		}
		b.WriteString(strings.Join(names, " / "))
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(r.Profile))
		b.WriteString("\n")
	}
	return storyctx.TaintedContent{Text: b.String(), Origin: llm.OriginUser}
}
