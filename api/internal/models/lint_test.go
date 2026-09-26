package models

import (
	"strings"
	"testing"

	"loomtale/api/internal/providers/image/comfyui"
)

const rev = "0123456789abcdef0123456789abcdef01234567"
const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validEntry() Entry {
	return Entry{
		Name: "tiny-model", Task: "scene", Title: "Tiny", Engine: "comfyui", Workflows: []string{"tiny"},
		Licence: Licence{SPDX: "Apache-2.0", URL: "https://example.com/licence", Verified: "2026-09-26"},
		Source:  Source{Repo: "org/tiny", Revision: rev},
		VRAMMB:  1000,
		Files: []File{
			{Path: "diffusion_models/tiny.safetensors", Remote: "tiny.safetensors", SHA256: sha, Size: 10},
			{Path: "vae/tiny_vae.safetensors", Remote: "vae.safetensors", SHA256: sha, Size: 10},
		},
	}
}

func tinyTemplates() map[string]*comfyui.Template {
	return map[string]*comfyui.Template{"tiny": {
		Name: "tiny",
		Graph: map[string]any{
			"1": map[string]any{"class_type": "UNETLoader", "inputs": map[string]any{"unet_name": "tiny.safetensors"}},
			"2": map[string]any{"class_type": "VAELoader", "inputs": map[string]any{"vae_name": "tiny_vae.safetensors"}},
		},
	}}
}

func lint(e Entry) []error {
	return Lint(&Manifest{Version: 1, Models: []Entry{e}}, tinyTemplates())
}

func expectProblem(t *testing.T, problems []error, want string) {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p.Error(), want) {
			return
		}
	}
	t.Fatalf("expected a lint problem containing %q, got %v", want, problems)
}

func TestLintAcceptsValidEntry(t *testing.T) {
	if problems := lint(validEntry()); len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
}

func TestLintRefusesPickleFiles(t *testing.T) {
	for _, ext := range []string{".bin", ".pt", ".ckpt", ".pth"} {
		e := validEntry()
		e.Files = append(e.Files, File{Path: "checkpoints/evil" + ext, Remote: "evil" + ext, SHA256: sha, Size: 1})
		expectProblem(t, lint(e), "pickle-format file refused")
	}
}

func TestLintRefusesMissingTransitiveFile(t *testing.T) {
	e := validEntry()
	e.Files = e.Files[:1] // drop the VAE the workflow loads
	expectProblem(t, lint(e), `loads "tiny_vae.safetensors", which is not in the entry's pinned file list`)
}

func TestLintRefusesUnpinnedRevisionAndBadChecksum(t *testing.T) {
	e := validEntry()
	e.Source.Revision = "main"
	e.Files[0].SHA256 = "abc"
	problems := lint(e)
	expectProblem(t, problems, "pinned 40-character commit revision")
	expectProblem(t, problems, "sha256 must be 64 lowercase hex characters")
}

func TestLintRefusesPathEscapesAndUnknownWorkflow(t *testing.T) {
	e := validEntry()
	e.Files[0].Path = "../etc/passwd.json"
	e.Workflows = append(e.Workflows, "missing")
	problems := lint(e)
	expectProblem(t, problems, "clean relative path")
	expectProblem(t, problems, `workflow "missing" has no template`)
}

func TestLintRefusesConflictingPins(t *testing.T) {
	a := validEntry()
	b := validEntry()
	b.Name = "other-model"
	b.Files[1].SHA256 = strings.Repeat("f", 64)
	problems := Lint(&Manifest{Version: 1, Models: []Entry{a, b}}, tinyTemplates())
	expectProblem(t, problems, "pinned to two different checksums")
}
