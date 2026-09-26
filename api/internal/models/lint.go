package models

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"loomtale/api/internal/providers/image/comfyui"
	"loomtale/api/internal/providers/llm/ollama"
)

var (
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	gitSHA1Pattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	namePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)
)

// weightExtensions are the only weight formats allowed; none can execute
// code on load, unlike pickle-based formats. ONNX graphs are protobuf,
// .data is ONNX external tensor data, and .npz is loaded with pickles
// disabled (numpy's default).
var weightExtensions = []string{".safetensors", ".gguf", ".onnx", ".data", ".npz"}

// formatCTranslate2 allows a .bin file that is a CTranslate2 model (a
// flat tensor file read by CTranslate2's own loader, never unpickled).
const formatCTranslate2 = "ctranslate2"

// maxGitPinnedSize bounds files pinned by git blob id: only small config
// files (for which the Hub publishes no sha256) may use that pin.
const maxGitPinnedSize = 16 << 20

// configExtensions are non-weight files a model may need (tokenizers,
// configs). They are data, never deserialized as code.
var configExtensions = []string{".json", ".txt", ".yaml"}

// pickleExtensions are refused with a dedicated message, since these are
// the formats an attacker-controlled upload would use to run code.
var pickleExtensions = []string{".bin", ".pt", ".pth", ".ckpt", ".pkl", ".pickle"}

// knownTasks and knownEngines are the values the rest of the app routes
// on; anything else is a manifest typo.
var (
	knownTasks   = []string{"scene", "thumbnail", "charsheet", "tts", "align", "llm", "lora", "score", "depth"}
	knownEngines = []string{"comfyui", "pyworker", "ollama"}
)

// Lint checks m against the manifest rules, the workflow templates its
// ComfyUI entries reference and the Modelfiles (keyed by entry name) its
// Ollama entries are imported with. It returns every problem found, not
// just the first, so one run shows the whole list.
func Lint(m *Manifest, templates map[string]*comfyui.Template, modelfiles map[string]string) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	seen := map[string]bool{}
	pathSHA := map[string]string{}
	for i, e := range m.Models {
		where := fmt.Sprintf("models[%d] %q", i, e.Name)
		if !namePattern.MatchString(e.Name) {
			add("%s: name must be lowercase letters, digits, dots and dashes", where)
		}
		if seen[e.Name] {
			add("%s: duplicate name", where)
		}
		seen[e.Name] = true
		if !slices.Contains(knownTasks, e.Task) {
			add("%s: unknown task %q", where, e.Task)
		}
		if !slices.Contains(knownEngines, e.Engine) {
			add("%s: unknown engine %q", where, e.Engine)
		}
		if e.Title == "" {
			add("%s: title is required", where)
		}
		if e.Licence.SPDX == "" || e.Licence.URL == "" {
			add("%s: licence spdx and url are required", where)
		}
		if _, err := time.Parse(time.DateOnly, e.Licence.Verified); err != nil {
			add("%s: licence.verified must be a YYYY-MM-DD date", where)
		}
		if e.Source.Repo == "" || !revisionPattern.MatchString(e.Source.Revision) {
			add("%s: source needs a repo and a pinned 40-character commit revision", where)
		}
		if e.VRAMMB <= 0 {
			add("%s: vram_mb must be positive", where)
		}
		if len(e.Files) == 0 {
			add("%s: at least one file is required", where)
		}
		for j, f := range e.Files {
			errs = append(errs, lintFile(fmt.Sprintf("%s files[%d] %q", where, j, f.Path), f, e.Source)...)
			if prev, ok := pathSHA[f.Path]; ok && prev != f.Digest() {
				add("%s files[%d]: %s is pinned to two different checksums across entries", where, j, f.Path)
			}
			pathSHA[f.Path] = f.Digest()
		}
		errs = append(errs, lintWorkflows(where, e, templates)...)
		errs = append(errs, lintModelfile(where, e, modelfiles)...)
	}
	return errs
}

func lintFile(where string, f File, src Source) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	clean := path.Clean(f.Path)
	if f.Path == "" || clean != f.Path || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, ".") {
		add("%s: path must be a clean relative path inside the models volume", where)
	}
	ext := strings.ToLower(path.Ext(f.Path))
	switch {
	case f.Format != "" && f.Format != formatCTranslate2:
		add("%s: unknown format %q", where, f.Format)
	case f.Format == formatCTranslate2 && ext != ".bin":
		add("%s: format %s is only for a .bin CTranslate2 model file", where, formatCTranslate2)
	case f.Format == formatCTranslate2:
		// A CTranslate2 model file: allowed despite its .bin extension.
	case slices.Contains(pickleExtensions, ext):
		add("%s: pickle-format file refused (only safetensors, GGUF, ONNX, npz and CTranslate2 weights are allowed)", where)
	case !slices.Contains(weightExtensions, ext) && !slices.Contains(configExtensions, ext):
		add("%s: file type %q is not allowed", where, ext)
	}
	if remoteExt := strings.ToLower(path.Ext(f.Remote)); f.Remote == "" || remoteExt != ext {
		add("%s: remote path is required and must have the same extension as path", where)
	}
	switch {
	case f.SHA256 != "" && f.GitSHA1 != "":
		add("%s: pin either sha256 or git_sha1, not both", where)
	case f.GitSHA1 != "":
		if !gitSHA1Pattern.MatchString(f.GitSHA1) {
			add("%s: git_sha1 must be 40 lowercase hex characters", where)
		}
		if !slices.Contains(configExtensions, ext) || f.Size > maxGitPinnedSize {
			add("%s: git_sha1 pins are only for config files up to %d bytes; weights need a sha256", where, maxGitPinnedSize)
		}
	case !sha256Pattern.MatchString(f.SHA256):
		add("%s: sha256 must be 64 lowercase hex characters", where)
	}
	if f.Size <= 0 {
		add("%s: size must be positive", where)
	}
	if l := f.Licence; l != nil {
		if l.SPDX == "" || l.URL == "" {
			add("%s: a file licence needs spdx and url", where)
		}
		if _, err := time.Parse(time.DateOnly, l.Verified); err != nil {
			add("%s: file licence.verified must be a YYYY-MM-DD date", where)
		}
	}
	repo, rev := f.RepoAndRevision(src)
	if repo == "" || !revisionPattern.MatchString(rev) {
		add("%s: file needs a repo and a pinned 40-character commit revision", where)
	}
	return errs
}

// lintWorkflows enforces the transitive file list: every model file a
// workflow's loader nodes reference must be pinned in the same entry.
func lintWorkflows(where string, e Entry, templates map[string]*comfyui.Template) []error {
	var errs []error
	if e.Engine != "comfyui" {
		return nil
	}
	names := e.FileNames()
	for _, wf := range e.Workflows {
		tpl, ok := templates[wf]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: workflow %q has no template in comfyui/workflows", where, wf))
			continue
		}
		for _, ref := range tpl.ReferencedFiles() {
			if _, ok := names[ref]; !ok {
				errs = append(errs, fmt.Errorf("%s: workflow %q loads %q, which is not in the entry's pinned file list", where, wf, ref))
			}
		}
	}
	return errs
}

// lintModelfile checks an Ollama entry's Modelfile: it must exist, parse,
// and import exactly one of the entry's pinned GGUF files from the models
// volume, so the offline import never reads a file the app has not
// verified.
func lintModelfile(where string, e Entry, modelfiles map[string]string) []error {
	if e.Engine != "ollama" {
		return nil
	}
	text, ok := modelfiles[e.Name]
	if !ok {
		return []error{fmt.Errorf("%s: ollama entry has no models/ollama/%s.Modelfile", where, e.Name)}
	}
	mf, err := ollama.ParseModelfile(text)
	if err != nil {
		return []error{fmt.Errorf("%s: Modelfile: %w", where, err)}
	}
	if _, ok := e.GGUFFile(mf.From); !ok {
		return []error{fmt.Errorf("%s: Modelfile FROM %s is not one of the entry's pinned .gguf files under %s", where, mf.From, ollama.ModelsMountDir)}
	}
	return nil
}
