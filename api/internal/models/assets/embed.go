// Package assets embeds the model manifest and ComfyUI workflow
// templates into the Go binaries. The files here are generated copies of
// models/manifest.yaml and comfyui/workflows/*.json (`make gen`, checked
// by `make gen-check`); edit the originals, never these copies.
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
