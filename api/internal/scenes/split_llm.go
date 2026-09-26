package scenes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// sceneSplitSchema constrains the llm.scene_split reply. The model sees
// paragraphs as P1..Pn and quoted lines as Q1..Qm and answers with those
// labels plus character names; it is never shown an id.
const sceneSplitSchema = `{
  "type": "object",
  "required": ["scenes", "speakers"],
  "additionalProperties": false,
  "properties": {
    "scenes": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["start", "imagePrompt", "characters"],
        "additionalProperties": false,
        "properties": {
          "start": {"type": "string", "pattern": "^P[0-9]+$"},
          "imagePrompt": {"type": "string", "minLength": 1, "maxLength": 600},
          "characters": {"type": "array", "items": {"type": "string", "maxLength": 100}, "maxItems": 12}
        }
      }
    },
    "speakers": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["quote", "speaker"],
        "additionalProperties": false,
        "properties": {
          "quote": {"type": "string", "pattern": "^Q[0-9]+$"},
          "speaker": {"type": "string", "maxLength": 100}
        }
      }
    }
  }
}`

// LLMSplit is the decoded llm.scene_split reply.
type LLMSplit struct {
	Scenes []struct {
		Start       string   `json:"start"`
		ImagePrompt string   `json:"imagePrompt"`
		Characters  []string `json:"characters"`
	} `json:"scenes"`
	Speakers []struct {
		Quote   string `json:"quote"`
		Speaker string `json:"speaker"`
	} `json:"speakers"`
}

// splitPrompt is the labelled view of a draft the model splits.
type splitPrompt struct {
	Paragraphs []Paragraph
	// quoteLabel maps (paragraph index, quote number) to "Q<k>".
	quoteLabel map[[2]int]string
	Text       string
}

// buildSplitPrompt labels every non-empty paragraph P1..Pn and every
// quoted line Q1..Qm, as one text for a data block.
func buildSplitPrompt(paragraphs []Paragraph) splitPrompt {
	sp := splitPrompt{quoteLabel: map[[2]int]string{}}
	var b, quotes strings.Builder
	b.WriteString("PARAGRAPHS\n")
	q := 0
	for _, p := range paragraphs {
		if strings.TrimSpace(p.Text) == "" {
			continue
		}
		pi := len(sp.Paragraphs)
		sp.Paragraphs = append(sp.Paragraphs, p)
		fmt.Fprintf(&b, "[P%d] %s\n", pi+1, strings.TrimSpace(p.Text))
		n := 0
		for _, span := range SplitSpans(p.Text) {
			if !span.Quote {
				continue
			}
			q++
			label := "Q" + strconv.Itoa(q)
			sp.quoteLabel[[2]int{pi, n}] = label
			fmt.Fprintf(&quotes, "[%s] (in P%d) %s\n", label, pi+1, span.Text)
			n++
		}
	}
	if quotes.Len() > 0 {
		b.WriteString("\nQUOTED LINES\n")
		b.WriteString(quotes.String())
	}
	sp.Text = b.String()
	return sp
}

// NormalizeLLMSplit turns the model's labelled reply into scenes. Scene
// boundaries come only from each scene's start label, so every paragraph
// lands in exactly one scene, in order, whatever the model got wrong
// (unknown labels, duplicates, gaps). Speaker and character names are
// resolved against the series' own characters; a name that does not
// resolve is voiced by the narrator and flagged. It returns the scenes
// and how many quoted lines had an unrecognised speaker.
func NormalizeLLMSplit(sp splitPrompt, out LLMSplit, idx NameIndex, lang string) ([]Draft, int) {
	n := len(sp.Paragraphs)
	if n == 0 {
		return nil, 0
	}
	type meta struct {
		prompt string
		chars  []string
	}
	starts := map[int]meta{0: {}}
	for _, s := range out.Scenes {
		k, err := strconv.Atoi(strings.TrimPrefix(s.Start, "P"))
		if err != nil || k < 1 || k > n {
			continue
		}
		if existing, ok := starts[k-1]; ok && (existing.prompt != "" || k-1 != 0) {
			continue // the first scene naming a start wins
		}
		starts[k-1] = meta{prompt: strings.TrimSpace(s.ImagePrompt), chars: s.Characters}
	}
	order := make([]int, 0, len(starts))
	for k := range starts {
		order = append(order, k)
	}
	sort.Ints(order)

	speakers := map[string]string{}
	for _, s := range out.Speakers {
		if _, ok := speakers[s.Quote]; !ok {
			speakers[s.Quote] = s.Speaker
		}
	}

	unrecognised := 0
	drafts := make([]Draft, 0, len(order))
	for i, start := range order {
		end := n
		if i+1 < len(order) {
			end = order[i+1]
		}
		group := sp.Paragraphs[start:end]
		base := start
		d := buildDraft(group, idx, lang, func(p Paragraph, quoteN int) (*uuid.UUID, string) {
			pi := base + indexOf(group, p.ID)
			name, ok := speakers[sp.quoteLabel[[2]int{pi, quoteN}]]
			if !ok {
				return nil, ""
			}
			id, flagged := idx.Speaker(name)
			if flagged != "" {
				unrecognised++
			}
			return id, flagged
		}, starts[start].prompt)
		d.CharacterIDs = mergeIDs(resolveNames(idx, starts[start].chars), d.CharacterIDs)
		drafts = append(drafts, d)
	}
	return drafts, unrecognised
}

func indexOf(ps []Paragraph, id string) int {
	for i, p := range ps {
		if p.ID == id {
			return i
		}
	}
	return 0
}

func resolveNames(idx NameIndex, names []string) []uuid.UUID {
	var out []uuid.UUID
	for _, n := range names {
		if id, ok := idx.Resolve(n); ok {
			out = append(out, id)
		}
	}
	return out
}

func mergeIDs(a, b []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, list := range [][]uuid.UUID{a, b} {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// cadenceInstruction is the server-built split setting sent to the model.
func cadenceInstruction(c Cadence, lang string) string {
	wpm := 150
	if lang == "vi" {
		wpm = 165
	}
	return fmt.Sprintf("Start a new scene about every %d to %d seconds of narration (roughly %d to %d words), where the image should change: a new place, time, action or speaker focus. Every scene starts at a paragraph.",
		c.MinS, c.MaxS, c.MinS*wpm/60, c.MaxS*wpm/60)
}
