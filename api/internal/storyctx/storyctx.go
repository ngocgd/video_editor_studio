// Package storyctx assembles the provider-agnostic llm.Request pieces for
// every AI writing action: a fixed server System template (never story
// content) plus every piece of story content as a separate llm.DataBlock,
// which api/internal/providers/llm.RenderDataBlocks later nonce-fences so
// untrusted content can never forge a closing fence or be mistaken for
// instructions. Nothing derived from story content is ever placed in
// System; the user's own free-text instruction is its own capped,
// origin=user block, never concatenated into System either.
//
// Reused by phase 7's scene split and phase 10's metadata/thumbnail text,
// which is why Build takes a generic BuildRequest rather than anything
// episode/draft-shaped.
package storyctx

import (
	"sort"

	"loomtale/api/internal/providers/llm"
)

// instructionMaxChars caps the user's own free-text instruction block.
const instructionMaxChars = 500

// charsPerToken is a coarse per-provider-family token estimate for
// latin/Cyrillic-script text (documented heuristic, not a real
// tokenizer): roughly 4 characters per token for English/Vietnamese
// prose. CJK text (Chinese chapter imports) tokenizes far denser than
// this, so budgeting slightly over-counts there, which is the safe
// direction (it truncates sooner rather than overflowing the provider's
// real context window).
const charsPerToken = 4

// Priority order (highest kept first) when truncating to fit the token
// budget, per the phase contract: target/selected text > user
// instruction > previously-summary > bible excerpt.
const (
	priorityTarget = iota
	priorityInstruction
	priorityPreviously
	priorityBible
)

// Block is one piece of content destined for a DataBlock, tagged with the
// truncation priority Build uses when the budget is tight.
type Block struct {
	Label    string
	Text     string
	Origin   llm.DataOrigin
	Tainted  bool
	priority int
}

// TaintedContent is one piece of story content with its own provenance,
// as already carried by the caller's DB row (bible section, draft
// paragraph, or draft summary).
type TaintedContent struct {
	Text    string
	Origin  llm.DataOrigin
	Tainted bool
}

// BuildRequest is the input to Build: every optional piece of context a
// caller may have for an action. A zero-value (empty Text) field is
// simply omitted from the resulting Context.
type BuildRequest struct {
	// BibleExcerpt is the relevant story-bible section content (e.g. the
	// glossary or style guide), already selected by the caller.
	BibleExcerpt TaintedContent

	// Previously is the rolling "Previously" summary text.
	Previously TaintedContent

	// Target is the selected/target story text the action operates on
	// (e.g. the paragraphs being rewritten, or the beat being expanded).
	Target TaintedContent

	// Instruction is the user's own free-text instruction. It is
	// truncated to instructionMaxChars and never merged into System.
	// Always origin=user, never tainted.
	Instruction string

	// TokenBudget is the resolved provider's context window, in tokens.
	// Zero means unbounded (no truncation).
	TokenBudget int
}

// Context is the assembled, provider-ready result: System is a fixed
// server template (constant per action, never story content), Data is the
// nonce-fenceable block list, and Tainted is true if any included block
// was tainted.
type Context struct {
	System      string
	Data        []llm.DataBlock
	Instruction string
	Tainted     bool
}

// Build assembles System (the fixed template for action) and every
// non-empty piece of req into separate DataBlocks, truncating
// lowest-priority blocks first to fit req.TokenBudget.
func Build(action string, req BuildRequest) Context {
	instruction := req.Instruction
	if len(instruction) > instructionMaxChars {
		instruction = truncateRunes(instruction, instructionMaxChars)
	}

	var blocks []Block
	if req.Target.Text != "" {
		blocks = append(blocks, Block{Label: "target", Text: req.Target.Text, Origin: req.Target.Origin, Tainted: req.Target.Tainted, priority: priorityTarget})
	}
	if instruction != "" {
		blocks = append(blocks, Block{Label: "instruction", Text: instruction, Origin: llm.OriginUser, Tainted: false, priority: priorityInstruction})
	}
	if req.Previously.Text != "" {
		blocks = append(blocks, Block{Label: "previously", Text: req.Previously.Text, Origin: req.Previously.Origin, Tainted: req.Previously.Tainted, priority: priorityPreviously})
	}
	if req.BibleExcerpt.Text != "" {
		blocks = append(blocks, Block{Label: "bible", Text: req.BibleExcerpt.Text, Origin: req.BibleExcerpt.Origin, Tainted: req.BibleExcerpt.Tainted, priority: priorityBible})
	}

	blocks = fitBudget(blocks, req.TokenBudget)

	data := make([]llm.DataBlock, 0, len(blocks))
	tainted := false
	for _, b := range blocks {
		data = append(data, llm.DataBlock{Label: b.Label, Text: b.Text, Origin: b.Origin, Tainted: b.Tainted})
		if b.Tainted {
			tainted = true
		}
	}

	return Context{
		System:      systemTemplate(action),
		Data:        data,
		Instruction: instruction,
		Tainted:     tainted,
	}
}

// fitBudget drops or truncates lowest-priority blocks first until the
// total estimated token count fits budget. budget<=0 means unbounded.
func fitBudget(blocks []Block, budget int) []Block {
	if budget <= 0 {
		return blocks
	}
	maxChars := budget * charsPerToken

	ordered := make([]Block, len(blocks))
	copy(ordered, blocks)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].priority < ordered[j].priority })

	total := 0
	kept := make([]Block, 0, len(ordered))
	for _, b := range ordered {
		remaining := maxChars - total
		if remaining <= 0 {
			continue
		}
		if len(b.Text) > remaining {
			b.Text = truncateRunes(b.Text, remaining)
		}
		total += len(b.Text)
		kept = append(kept, b)
	}

	// Restore original relative order (target, instruction, previously,
	// bible) so the rendered prompt is stable/deterministic regardless of
	// which blocks got dropped.
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].priority < kept[j].priority })
	return kept
}

// truncateRunes truncates s to at most n bytes without splitting a
// multi-byte rune.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	b := []byte(s)[:n]
	for len(b) > 0 && !isRuneStart(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return string(b)
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// systemTemplate returns the fixed server system string for action. It is
// a Go constant per action: never interpolates story content.
func systemTemplate(action string) string {
	tmpl, ok := systemTemplates[action]
	if !ok {
		return systemTemplateDefault
	}
	return tmpl
}
