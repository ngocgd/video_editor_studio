package comfyui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"loomtale/api/internal/pipeline"
)

// ParamSpec maps one named workflow parameter to a node input.
type ParamSpec struct {
	Node     string `json:"node"`
	Input    string `json:"input"`
	Required bool   `json:"required"`
	// Kind "image" marks an input that takes an uploaded image file
	// name: the Engine uploads the bytes and fills in the name.
	Kind string `json:"kind"`
	// BypassInput makes the parameter's node optional: when the
	// parameter is not supplied, the node is removed and every link to
	// its output is rewired to the node's own BypassInput link (e.g. a
	// LoRA loader bypassed via its "model" input).
	BypassInput string `json:"bypass_input"`
}

// ParamMap is a workflow's <name>.params.json.
type ParamMap struct {
	Description string               `json:"description"`
	OutputNode  string               `json:"output_node"`
	Params      map[string]ParamSpec `json:"params"`
	// Warmup holds parameter overrides for the smallest run that still
	// loads every model the workflow uses (tiny size, one step). The
	// residency backend runs it to make a model resident.
	Warmup map[string]any `json:"warmup"`
}

// Template is one API-format workflow plus its parameter map.
type Template struct {
	Name  string
	Graph map[string]any
	Map   ParamMap
}

// ErrInvalidParams is a permanent (validation) error: retrying the same
// parameters can never succeed.
var ErrInvalidParams = fmt.Errorf("comfyui: invalid workflow parameters: %w", pipeline.ErrValidation)

// LoadTemplates reads every "<name>.params.json" under dir in fsys and
// its "<name>.json" graph. Graph files without a parameter map (the
// phase 1b smoke graphs) are ignored.
func LoadTemplates(fsys fs.FS, dir string) (map[string]*Template, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("comfyui: read workflows: %w", err)
	}
	out := map[string]*Template{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".params.json")
		if !ok {
			continue
		}
		tpl := &Template{Name: name}
		if err := readJSON(fsys, path.Join(dir, entry.Name()), &tpl.Map); err != nil {
			return nil, err
		}
		if err := readJSON(fsys, path.Join(dir, name+".json"), &tpl.Graph); err != nil {
			return nil, err
		}
		if err := tpl.validate(); err != nil {
			return nil, err
		}
		out[name] = tpl
	}
	return out, nil
}

func readJSON(fsys fs.FS, name string, v any) error {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("comfyui: read %s: %w", name, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("comfyui: decode %s: %w", name, err)
	}
	return nil
}

// validate checks the parameter map only points at nodes that exist.
func (t *Template) validate() error {
	if _, ok := t.Graph[t.Map.OutputNode]; !ok {
		return fmt.Errorf("comfyui: workflow %s: output node %q does not exist", t.Name, t.Map.OutputNode)
	}
	for name, spec := range t.Map.Params {
		if _, ok := t.Graph[spec.Node]; !ok {
			return fmt.Errorf("comfyui: workflow %s: param %q targets missing node %q", t.Name, name, spec.Node)
		}
	}
	for name := range t.Map.Warmup {
		if _, ok := t.Map.Params[name]; !ok {
			return fmt.Errorf("comfyui: workflow %s: warmup sets unknown param %q", t.Name, name)
		}
	}
	return nil
}

// loaderInputs are the node inputs that name a model file on the models
// volume, across the loader nodes the workflows use.
var loaderInputs = []string{"unet_name", "clip_name", "clip_name1", "clip_name2", "vae_name", "ckpt_name", "lora_name"}

// ReferencedFiles returns every model file name the graph's loader nodes
// reference, sorted. An empty value (a LoRA slot filled at run time) is
// not a reference.
func (t *Template) ReferencedFiles() []string {
	seen := map[string]bool{}
	for _, raw := range t.Graph {
		node, _ := raw.(map[string]any)
		inputs, _ := node["inputs"].(map[string]any)
		for _, key := range loaderInputs {
			if v, ok := inputs[key].(string); ok && v != "" {
				seen[v] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ImageParams returns the names of the parameters that take an uploaded
// image.
func (t *Template) ImageParams() []string {
	var out []string
	for name, spec := range t.Map.Params {
		if spec.Kind == "image" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Build returns a fresh copy of the graph with params applied. Unknown
// parameter names and missing required ones are rejected; an optional
// node whose bypass parameter is absent is removed and bypassed.
func (t *Template) Build(params map[string]any) (map[string]any, error) {
	for name := range params {
		if _, ok := t.Map.Params[name]; !ok {
			return nil, fmt.Errorf("%w: workflow %s has no parameter %q", ErrInvalidParams, t.Name, name)
		}
	}
	for name, spec := range t.Map.Params {
		if _, ok := params[name]; spec.Required && !ok {
			return nil, fmt.Errorf("%w: workflow %s requires parameter %q", ErrInvalidParams, t.Name, name)
		}
	}

	graph, err := deepCopy(t.Graph)
	if err != nil {
		return nil, err
	}

	removed := map[string]bool{}
	for name, spec := range t.Map.Params {
		if spec.BypassInput == "" {
			continue
		}
		if v, ok := params[name]; ok && v != "" {
			continue
		}
		if err := bypassNode(graph, spec.Node, spec.BypassInput); err != nil {
			return nil, fmt.Errorf("comfyui: workflow %s: %w", t.Name, err)
		}
		removed[spec.Node] = true
	}

	for name, value := range params {
		spec := t.Map.Params[name]
		if removed[spec.Node] {
			continue
		}
		node := graph[spec.Node].(map[string]any)
		inputs, _ := node["inputs"].(map[string]any)
		if inputs == nil {
			inputs = map[string]any{}
			node["inputs"] = inputs
		}
		inputs[spec.Input] = value
	}
	return graph, nil
}

// bypassNode deletes nodeID and points every link to its first output at
// the link the node itself received on bypassInput.
func bypassNode(graph map[string]any, nodeID, bypassInput string) error {
	node, _ := graph[nodeID].(map[string]any)
	inputs, _ := node["inputs"].(map[string]any)
	upstream, ok := inputs[bypassInput].([]any)
	if !ok {
		return fmt.Errorf("node %s has no link on input %q to bypass through", nodeID, bypassInput)
	}
	delete(graph, nodeID)
	for _, raw := range graph {
		other, _ := raw.(map[string]any)
		otherInputs, _ := other["inputs"].(map[string]any)
		for key, v := range otherInputs {
			link, ok := v.([]any)
			if ok && len(link) == 2 && link[0] == nodeID {
				otherInputs[key] = upstream
			}
		}
	}
	return nil
}

func deepCopy(graph map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
