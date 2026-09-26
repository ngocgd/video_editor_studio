package models

import (
	"context"
	"fmt"
	"path/filepath"

	"loomtale/api/internal/providers/llm/ollama"
)

// OllamaPreparer makes a manifest LLM available to Ollama before it
// loads: it runs the load gate (licence plus verified files), then, if
// Ollama does not have the model yet, imports the pinned GGUF offline
// with the entry's Modelfile. It is wired as ollama.Backend.Prepare in
// the worker, the only process that mounts the models volume.
type OllamaPreparer struct {
	Manifest   *Manifest
	Modelfiles map[string]string
	Gate       func(ctx context.Context, name string) error
	Importer   *ollama.Importer
	// Dir is the models volume as mounted in this process.
	Dir string
}

// Prepare implements ollama.Backend.Prepare.
func (p *OllamaPreparer) Prepare(ctx context.Context, model string) error {
	e, ok := p.Manifest.Get(model)
	if !ok || e.Engine != "ollama" {
		return fmt.Errorf("%w: %q is not an Ollama model in the manifest", ErrNotInstalled, model)
	}
	if err := p.Gate(ctx, model); err != nil {
		return err
	}
	exists, err := p.Importer.Exists(ctx, model)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	mf, err := ollama.ParseModelfile(p.Modelfiles[model])
	if err != nil {
		return fmt.Errorf("models: %s Modelfile: %w", model, err)
	}
	f, ok := e.GGUFFile(mf.From)
	if !ok {
		return fmt.Errorf("models: %s Modelfile FROM %s is not a pinned file of the entry", model, mf.From)
	}
	return p.Importer.Import(ctx, model, mf, filepath.Join(p.Dir, filepath.FromSlash(f.Path)), f.SHA256)
}
