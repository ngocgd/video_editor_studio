package models

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"loomtale/api/internal/db/migrations"
)

// seedLLMDefaultMigration is the data migration that makes Ollama the
// default LLM once a local LLM candidate is installed.
const seedLLMDefaultMigration = "20260927600000_seed_llm_default.sql"

var seedCandidatesRe = regexp.MustCompile(`m\.name IN \(([^)]*)\)`)

// The seed migration gates on the manifest's local LLM candidates by name.
// A renamed, added or removed candidate would silently stop the seed, so
// the list in SQL must match the manifest exactly.
func TestSeedLLMDefaultMigrationNamesTheManifestLLMCandidates(t *testing.T) {
	raw, err := migrations.FS.ReadFile(seedLLMDefaultMigration)
	if err != nil {
		t.Fatal(err)
	}
	match := seedCandidatesRe.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s: no `m.name IN (...)` candidate list", seedLLMDefaultMigration)
	}
	var inSQL []string
	for _, part := range strings.Split(string(match[1]), ",") {
		inSQL = append(inSQL, strings.Trim(strings.TrimSpace(part), "'"))
	}

	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	var inManifest []string
	for _, name := range m.Names() {
		e, _ := m.Get(name)
		if e.Engine == "ollama" && e.Task == "llm" {
			inManifest = append(inManifest, name)
		}
	}
	if len(inManifest) == 0 {
		t.Fatal("manifest has no ollama llm entries")
	}

	slices.Sort(inSQL)
	slices.Sort(inManifest)
	if !slices.Equal(inSQL, inManifest) {
		t.Fatalf("seed migration candidates %v, manifest ollama llm entries %v", inSQL, inManifest)
	}
}
