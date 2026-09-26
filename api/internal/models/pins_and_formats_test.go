package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loomtale/api/internal/pipeline"
)

// gitBlobID of "hello\n" is the well-known id `git hash-object` prints.
const helloBlobID = "ce013625030ba8dba906f756967f9e9ca394464a"

func TestGitPinnedFileDownloadsAndVerifiesByBlobID(t *testing.T) {
	f := newFixture(t)
	f.hub.content["tokenizer.json"] = []byte("hello\n")
	f.entry.Files = append(f.entry.Files, File{Path: "tts/tiny/tokenizer.json", Remote: "tokenizer.json", GitSHA1: helloBlobID, Size: 6})
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(f.readFinal(t, "tts/tiny/tokenizer.json")); got != "hello\n" {
		t.Fatalf("content = %q", got)
	}
	digest, _, ok, _ := f.files.VerifiedFile(context.Background(), "tts/tiny/tokenizer.json")
	if !ok || digest != "git-sha1:"+helloBlobID {
		t.Fatalf("recorded digest = %q, %v", digest, ok)
	}
}

func TestGitPinnedFileWithWrongContentIsRefused(t *testing.T) {
	f := newFixture(t)
	f.hub.content["tokenizer.json"] = []byte("hullo\n")
	f.entry.Files = append(f.entry.Files, File{Path: "tts/tiny/tokenizer.json", Remote: "tokenizer.json", GitSHA1: helloBlobID, Size: 6})
	err := f.d.Install(context.Background(), f.entry, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected a checksum mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "tts/tiny/tokenizer.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file with the wrong blob id must never be moved into place")
	}
}

func TestGitPinnedFileOnDiskIsAdopted(t *testing.T) {
	f := newFixture(t)
	file := File{Path: "tts/tiny/tokenizer.json", Remote: "tokenizer.json", GitSHA1: helloBlobID, Size: 6}
	f.entry.Files = []File{file}
	final := filepath.Join(f.dir, "tts", "tiny", "tokenizer.json")
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Install(context.Background(), f.entry, nil); err != nil {
		t.Fatal(err)
	}
	if f.hub.requests != 0 {
		t.Fatalf("a matching file on disk must be adopted, not downloaded (%d requests)", f.hub.requests)
	}
}

func TestLintGitPinsOnlyForSmallConfigFiles(t *testing.T) {
	e := validEntry()
	e.Files = append(e.Files, File{Path: "text_encoders/tok.json", Remote: "tok.json", GitSHA1: helloBlobID, Size: 6})
	if problems := lint(e); len(problems) != 0 {
		t.Fatalf("a git-pinned config file should lint clean: %v", problems)
	}

	e = validEntry()
	e.Files[0].SHA256 = ""
	e.Files[0].GitSHA1 = helloBlobID
	expectProblem(t, lint(e), "git_sha1 pins are only for config files")

	e = validEntry()
	e.Files[0].GitSHA1 = helloBlobID
	expectProblem(t, lint(e), "not both")

	e = validEntry()
	e.Files = append(e.Files, File{Path: "text_encoders/big.json", Remote: "big.json", GitSHA1: helloBlobID, Size: maxGitPinnedSize + 1})
	expectProblem(t, lint(e), "git_sha1 pins are only for config files")
}

func TestLintAllowsCTranslate2BinOnlyWhenDeclared(t *testing.T) {
	e := validEntry()
	e.Files = append(e.Files, File{Path: "align/w/model.bin", Remote: "model.bin", SHA256: sha, Size: 1, Format: "ctranslate2"})
	if problems := lint(e); len(problems) != 0 {
		t.Fatalf("a declared CTranslate2 model should lint clean: %v", problems)
	}

	e = validEntry()
	e.Files = append(e.Files, File{Path: "align/w/model.bin", Remote: "model.bin", SHA256: sha, Size: 1})
	expectProblem(t, lint(e), "pickle-format file refused")

	e = validEntry()
	e.Files = append(e.Files, File{Path: "align/w/model.pt", Remote: "model.pt", SHA256: sha, Size: 1, Format: "ctranslate2"})
	expectProblem(t, lint(e), "only for a .bin CTranslate2 model file")

	e = validEntry()
	e.Files = append(e.Files, File{Path: "align/w/model.bin", Remote: "model.bin", SHA256: sha, Size: 1, Format: "pickle"})
	expectProblem(t, lint(e), `unknown format "pickle"`)
}

func TestLintAllowsOnnxAndNpzWeights(t *testing.T) {
	e := validEntry()
	for _, name := range []string{"graph.onnx", "graph.data", "heads.npz"} {
		e.Files = append(e.Files, File{Path: "tts/v/" + name, Remote: name, SHA256: sha, Size: 1})
	}
	if problems := lint(e); len(problems) != 0 {
		t.Fatalf("ONNX and npz weights should lint clean: %v", problems)
	}
}

func TestGateChecksPerFileLicences(t *testing.T) {
	e := validEntry()
	e.Files[1].Licence = &Licence{SPDX: "CC-BY-NC-4.0", URL: "https://example.com/nc", Verified: "2026-09-26"}
	err := Gate(e)
	if !errors.Is(err, pipeline.ErrLicenceRefused) || !strings.Contains(err.Error(), "vae/tiny_vae.safetensors") {
		t.Fatalf("a non-allowlisted file licence must refuse the entry, got %v", err)
	}

	e.Files[1].Licence = &Licence{SPDX: "Apache-2.0", URL: "https://example.com/apache", Verified: "2026-09-26"}
	if err := Gate(e); err != nil {
		t.Fatalf("an allowlisted file licence must pass: %v", err)
	}

	e.Files[1].Licence = &Licence{SPDX: "Apache-2.0", Verified: "not a date"}
	problems := lint(e)
	expectProblem(t, problems, "a file licence needs spdx and url")
	expectProblem(t, problems, "file licence.verified must be a YYYY-MM-DD date")
}

func ollamaEntry() Entry {
	return Entry{
		Name: "tiny-llm", Task: "llm", Title: "Tiny LLM", Engine: "ollama",
		Licence: Licence{SPDX: "Apache-2.0", URL: "https://example.com/licence", Verified: "2026-09-26"},
		Source:  Source{Repo: "org/tiny-llm", Revision: rev},
		VRAMMB:  1000,
		Files:   []File{{Path: "llm/tiny.gguf", Remote: "tiny.gguf", SHA256: sha, Size: 10}},
	}
}

func TestLintOllamaEntryNeedsAModelfileImportingItsGGUF(t *testing.T) {
	m := &Manifest{Version: 1, Models: []Entry{ollamaEntry()}}
	if problems := Lint(m, nil, map[string]string{"tiny-llm": "FROM /models/llm/tiny.gguf\nPARAMETER num_ctx 4096\n"}); len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	expectProblem(t, Lint(m, nil, nil), "has no models/ollama/tiny-llm.Modelfile")
	expectProblem(t, Lint(m, nil, map[string]string{"tiny-llm": "FROM /models/llm/other.gguf\n"}), "is not one of the entry's pinned .gguf files")
	expectProblem(t, Lint(m, nil, map[string]string{"tiny-llm": "FROM /models/llm/tiny.gguf\nADAPTER /tmp/x.gguf\n"}), `directive "ADAPTER" is not allowed`)
}
