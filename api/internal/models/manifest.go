// Package models owns the local model manifest (models/manifest.yaml):
// parsing, the linter, the licence gate, the resumable checksum-verified
// downloader, the install state store and the models.* pipeline steps.
// The manifest is the single source of truth for what a model is; the
// database only records whether it is installed.
package models

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // uuid v5 is defined over SHA-1; this is a stable name-derived id, not a security hash
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"loomtale/api/internal/models/assets"
	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/llm/ollama"
)

// Manifest is the parsed models/manifest.yaml.
type Manifest struct {
	Version int     `yaml:"version"`
	Models  []Entry `yaml:"models"`
}

// Entry is one model: everything needed to download, verify, licence
// check and load it.
type Entry struct {
	Name      string   `yaml:"name"`
	Task      string   `yaml:"task"`
	Title     string   `yaml:"title"`
	Engine    string   `yaml:"engine"`
	Workflows []string `yaml:"workflows"`
	Licence   Licence  `yaml:"licence"`
	Source    Source   `yaml:"source"`
	VRAMMB    int64    `yaml:"vram_mb"`
	Files     []File   `yaml:"files"`
}

// Licence is the upstream licence recorded for an entry.
type Licence struct {
	SPDX string `yaml:"spdx"`
	URL  string `yaml:"url"`
	// Verified is the date (YYYY-MM-DD) someone last checked the licence
	// at URL by hand.
	Verified string `yaml:"verified"`
}

// Source is the default repository and pinned revision files come from.
type Source struct {
	Repo     string `yaml:"repo"`
	Revision string `yaml:"revision"`
	// Base optionally names the upstream model the files were derived
	// from (repo@revision), for provenance only.
	Base string `yaml:"base"`
}

// File is one pinned file of an entry.
type File struct {
	// Path is relative to the models volume root and doubles as the
	// ComfyUI folder category (diffusion_models/, text_encoders/, ...).
	Path string `yaml:"path"`
	// Remote is the file's path inside the repository.
	Remote string `yaml:"remote"`
	// Repo/Revision override the entry's Source for files that live in
	// another repository (e.g. a shared text encoder).
	Repo     string `yaml:"repo"`
	Revision string `yaml:"revision"`
	// SHA256 pins the file's content. Small non-LFS config files, for
	// which the Hub publishes no sha256, are pinned by GitSHA1 instead:
	// the git blob id of the file at the pinned revision, which the
	// revision's own commit hash already covers. Exactly one is set.
	SHA256  string `yaml:"sha256"`
	GitSHA1 string `yaml:"git_sha1"`
	Size    int64  `yaml:"size"`
	// Format names a non-default weight format a file extension alone
	// cannot prove: "ctranslate2" allows a .bin that is a CTranslate2
	// model (a flat tensor file, not a pickle).
	Format string `yaml:"format"`
	// Licence overrides the entry's licence for a file that comes from a
	// repository under another licence (e.g. an alignment model bundled
	// with a speech recogniser). The licence gate checks every file.
	Licence *Licence `yaml:"licence"`
}

// gitSHA1Prefix marks a digest that is a git blob id rather than a
// sha256, wherever digests are stored or compared.
const gitSHA1Prefix = "git-sha1:"

// Digest is f's pinned content digest as stored in model_files: the
// sha256 hex, or "git-sha1:<hex>" for a file pinned by its git blob id.
func (f File) Digest() string {
	if f.GitSHA1 != "" {
		return gitSHA1Prefix + f.GitSHA1
	}
	return f.SHA256
}

// RepoAndRevision resolves f's effective repository and revision.
func (f File) RepoAndRevision(src Source) (string, string) {
	repo, rev := src.Repo, src.Revision
	if f.Repo != "" {
		repo = f.Repo
	}
	if f.Revision != "" {
		rev = f.Revision
	}
	return repo, rev
}

// SizeBytes is the total size of every file in e.
func (e Entry) SizeBytes() int64 {
	var total int64
	for _, f := range e.Files {
		total += f.Size
	}
	return total
}

// FileNames returns the base names of e's files, which is how ComfyUI
// workflows reference them (unet_name, clip_name, vae_name, lora_name).
func (e Entry) FileNames() map[string]File {
	out := make(map[string]File, len(e.Files))
	for _, f := range e.Files {
		out[path.Base(f.Path)] = f
	}
	return out
}

// Parse decodes and structurally validates raw manifest YAML. Unknown
// keys are rejected so a typo (e.g. "sha265") can never silently drop a
// pin.
func Parse(raw []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("models: parse manifest: %w", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("models: unsupported manifest version %d", m.Version)
	}
	return &m, nil
}

// Embedded returns the manifest compiled into this binary (copied from
// models/manifest.yaml by `make gen`).
func Embedded() (*Manifest, error) {
	return Parse(assets.Manifest)
}

// Get returns the entry named name.
func (m *Manifest) Get(name string) (Entry, bool) {
	for _, e := range m.Models {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Names returns every entry name, sorted.
func (m *Manifest) Names() []string {
	out := make([]string, 0, len(m.Models))
	for _, e := range m.Models {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// VRAMByRef maps "engine:name" to the planned VRAM ceiling, the shape
// residency.Manager.Manifests expects.
func (m *Manifest) VRAMByRef() map[string]int64 {
	out := make(map[string]int64, len(m.Models))
	for _, e := range m.Models {
		out[e.Engine+":"+e.Name] = e.VRAMMB
	}
	return out
}

// scopeNamespace is the fixed uuid v5 namespace for model scope ids.
var scopeNamespace = uuid.MustParse("5b0f1f3e-2d7c-4f53-9d0a-6c1f8b7e2a10")

// ScopeID is the pipeline scope id for a model: pipeline rows need a
// uuid scope, and models are keyed by name, so the id is derived from
// the name deterministically (uuid v5) and mapped back with ScopeName.
func ScopeID(name string) uuid.UUID {
	return uuid.NewHash(sha1.New(), scopeNamespace, []byte(name), 5)
}

// ScopeName maps a scope id back to its entry name.
func (m *Manifest) ScopeName(id uuid.UUID) (string, bool) {
	for _, e := range m.Models {
		if ScopeID(e.Name) == id {
			return e.Name, true
		}
	}
	return "", false
}

// ScopeKind is the pipeline scope_kind used by every models.* step.
const ScopeKind = "model"

// EmbeddedTemplates loads the ComfyUI workflow templates compiled into
// this binary.
func EmbeddedTemplates() (map[string]*comfyui.Template, error) {
	return comfyui.LoadTemplates(assets.Workflows, "workflows")
}

// EmbeddedModelfiles loads the Ollama Modelfiles compiled into this
// binary, keyed by entry name.
func EmbeddedModelfiles() (map[string]string, error) {
	return LoadModelfiles(assets.Modelfiles, "ollama")
}

// LoadModelfiles reads every <name>.Modelfile in dir of fsys.
func LoadModelfiles(fsys fs.FS, dir string) (map[string]string, error) {
	matches, err := fs.Glob(fsys, path.Join(dir, "*.Modelfile"))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(matches))
	for _, m := range matches {
		raw, err := fs.ReadFile(fsys, m)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(path.Base(m), ".Modelfile")] = string(raw)
	}
	return out, nil
}

// Warmups maps every ComfyUI model to the workflow that loads it (its
// first workflow), the shape comfyui.Backend.Warmups expects.
func (m *Manifest) Warmups() map[string]string {
	out := map[string]string{}
	for _, e := range m.Models {
		if e.Engine == "comfyui" && len(e.Workflows) > 0 {
			out[e.Name] = e.Workflows[0]
		}
	}
	return out
}

// GGUFFile resolves an Ollama Modelfile's FROM path (a file under the
// models volume as mounted at ollama.ModelsMountDir) to one of e's
// pinned .gguf files.
func (e Entry) GGUFFile(from string) (File, bool) {
	rel, ok := strings.CutPrefix(from, ollama.ModelsMountDir+"/")
	if !ok {
		return File{}, false
	}
	for _, f := range e.Files {
		if f.Path == rel && strings.EqualFold(path.Ext(f.Path), ".gguf") {
			return f, true
		}
	}
	return File{}, false
}
