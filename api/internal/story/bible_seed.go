package story

import (
	"encoding/json"
	"fmt"
)

// glossarySection is the bible section whose content is JSON-encoded
// array text rather than prose (see BibleSectionUpdateRequest.content).
const glossarySection = "glossary"

// glossaryTerm is one preferred term rendering, in the field order the
// bible editor's glossary table writes, so a seeded glossary and a saved
// one are the same text.
type glossaryTerm struct {
	TermZh string `json:"termZh"`
	En     string `json:"en"`
	Vi     string `json:"vi"`
}

// bibleSeedSections turns a schema-validated llm.bible_seed response into
// section name -> stored content. Prose sections are stored as returned;
// the glossary array is stored as its JSON text, the format the bible
// editor parses, so the seeded terms show up in the glossary table.
func bibleSeedSections(text string) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("story: parse bible_seed response: %w", err)
	}
	sections := make(map[string]string, len(raw))
	for name, value := range raw {
		if name == glossarySection {
			var terms []glossaryTerm
			if err := json.Unmarshal(value, &terms); err != nil {
				return nil, fmt.Errorf("story: parse bible_seed glossary: %w", err)
			}
			if terms == nil {
				terms = []glossaryTerm{}
			}
			encoded, err := json.Marshal(terms)
			if err != nil {
				return nil, err
			}
			sections[name] = string(encoded)
			continue
		}
		var content string
		if err := json.Unmarshal(value, &content); err != nil {
			return nil, fmt.Errorf("story: parse bible_seed section %q: %w", name, err)
		}
		sections[name] = content
	}
	return sections, nil
}
