package story

import (
	"encoding/json"
	"errors"
	"testing"

	"loomtale/api/internal/providers/llm"
)

const seedResponseWithTerms = `{
  "world": "A mountain sect.",
  "cultivation_realms": "Qi Refining, Foundation",
  "arcs": "Outer disciple to elder.",
  "style_guide": "Short sentences.",
  "running_summary": "",
  "glossary": [
    {"termZh": "气", "en": "Qi", "vi": "Khí"},
    {"termZh": "宗门", "en": "Sect", "vi": "Tông môn"}
  ]
}`

func TestBibleSeedGlossaryIsStoredAsTheEditorsTermArray(t *testing.T) {
	if err := llm.ValidateJSON(bibleSeedSchema, seedResponseWithTerms); err != nil {
		t.Fatalf("seed response should satisfy the schema: %v", err)
	}
	sections, err := bibleSeedSections(seedResponseWithTerms)
	if err != nil {
		t.Fatal(err)
	}
	if sections["world"] != "A mountain sect." {
		t.Fatalf("world = %q", sections["world"])
	}
	var terms []glossaryTerm
	if err := json.Unmarshal([]byte(sections["glossary"]), &terms); err != nil {
		t.Fatalf("glossary content is not a JSON term array: %q (%v)", sections["glossary"], err)
	}
	want := []glossaryTerm{{"气", "Qi", "Khí"}, {"宗门", "Sect", "Tông môn"}}
	if len(terms) != len(want) || terms[0] != want[0] || terms[1] != want[1] {
		t.Fatalf("terms = %+v, want %+v", terms, want)
	}
	if got := sections["glossary"]; got != `[{"termZh":"气","en":"Qi","vi":"Khí"},{"termZh":"宗门","en":"Sect","vi":"Tông môn"}]` {
		t.Fatalf("glossary text = %s, want the editor's compact field order", got)
	}
}

func TestBibleSeedEmptyGlossaryIsAnEmptyArray(t *testing.T) {
	sections, err := bibleSeedSections(`{"world":"w","cultivation_realms":"","arcs":"","style_guide":"","running_summary":"","glossary":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if sections["glossary"] != "[]" {
		t.Fatalf("glossary = %q, want []", sections["glossary"])
	}
}

func TestBibleSeedSchemaRejectsAProseGlossary(t *testing.T) {
	prose := `{"world":"w","cultivation_realms":"","arcs":"","style_guide":"","running_summary":"","glossary":"Qi: life energy."}`
	if err := llm.ValidateJSON(bibleSeedSchema, prose); !errors.Is(err, llm.ErrSchemaValidation) {
		t.Fatalf("ValidateJSON = %v, want a schema validation error", err)
	}
}
