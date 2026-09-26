// Package assets embeds the model manifest, the ComfyUI workflow
// templates and the Ollama Modelfiles into the Go binaries. The files
// here are generated copies of models/manifest.yaml,
// comfyui/workflows/*.json and models/ollama/*.Modelfile (`make gen`,
// checked by `make gen-check`); edit the originals, never these copies.
package assets

import "embed"

// Manifest is the embedded copy of models/manifest.yaml.
//
//go:embed manifest.yaml
var Manifest []byte

// Workflows holds the embedded copies of comfyui/workflows/*.json, under
// "workflows/".
//
//go:embed workflows/*.json
var Workflows embed.FS

// Modelfiles holds the embedded copies of models/ollama/*.Modelfile,
// under "ollama/".
//
//go:embed ollama/*.Modelfile
var Modelfiles embed.FS
