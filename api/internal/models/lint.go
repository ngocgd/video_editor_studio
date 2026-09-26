package models

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"loomtale/api/internal/providers/image/comfyui"
)

var (
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	namePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)
)

// weightExtensions are the only weight formats allowed: neither can
// execute code on load, unlike pickle-based formats.
var weightExtensions = []string{".safetensors", ".gguf"}

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

// Lint checks m against the manifest rules and the workflow templates it
// references. It returns every problem found, not just the first, so one
// run shows the whole list.
func Lint(m *Manifest, templates map[string]*comfyui.Template) []error {
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
			if prev, ok := pathSHA[f.Path]; ok && prev != f.SHA256 {
				add("%s files[%d]: %s is pinned to two different checksums across entries", where, j, f.Path)
			}
			pathSHA[f.Path] = f.SHA256
		}
		errs = append(errs, lintWorkflows(where, e, templates)...)
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
	case slices.Contains(pickleExtensions, ext):
		add("%s: pickle-format file refused (only .safetensors and .gguf weights are allowed)", where)
	case !slices.Contains(weightExtensions, ext) && !slices.Contains(configExtensions, ext):
		add("%s: file type %q is not allowed", where, ext)
	}
	if remoteExt := strings.ToLower(path.Ext(f.Remote)); f.Remote == "" || remoteExt != ext {
		add("%s: remote path is required and must have the same extension as path", where)
	}
	if !sha256Pattern.MatchString(f.SHA256) {
		add("%s: sha256 must be 64 lowercase hex characters", where)
	}
	if f.Size <= 0 {
		add("%s: size must be positive", where)
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
