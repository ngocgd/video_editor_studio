package comfyui

import (
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"loomtale/api/internal/pipeline"
)

const sceneGraph = `{
  "1": {"class_type": "UNETLoader", "inputs": {"unet_name": "unet.safetensors"}},
  "2": {"class_type": "CLIPLoader", "inputs": {"clip_name": "te.safetensors"}},
  "11": {"class_type": "LoraLoaderModelOnly", "inputs": {"model": ["1", 0], "lora_name": "", "strength_model": 1.0}},
  "4": {"class_type": "CLIPTextEncode", "inputs": {"clip": ["2", 0], "text": ""}},
  "7": {"class_type": "ModelSamplingAuraFlow", "inputs": {"model": ["11", 0], "shift": 3}},
  "8": {"class_type": "KSampler", "inputs": {"model": ["7", 0], "seed": 0}},
  "10": {"class_type": "SaveImage", "inputs": {"images": ["8", 0]}}
}`

const sceneParams = `{
  "output_node": "10",
  "params": {
    "prompt": {"node": "4", "input": "text", "required": true},
    "seed": {"node": "8", "input": "seed"},
    "lora_name": {"node": "11", "input": "lora_name", "bypass_input": "model"},
    "lora_strength": {"node": "11", "input": "strength_model"}
  },
  "warmup": {"seed": 1}
}`

func loadScene(t *testing.T) *Template {
	t.Helper()
	fsys := fstest.MapFS{
		"wf/scene.json":        {Data: []byte(sceneGraph)},
		"wf/scene.params.json": {Data: []byte(sceneParams)},
		"wf/smoke-only.json":   {Data: []byte(`{}`)},
	}
	templates, err := LoadTemplates(fsys, "wf")
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 {
		t.Fatalf("a graph without a param map must be ignored, got %d templates", len(templates))
	}
	return templates["scene"]
}

func input(graph map[string]any, node, key string) any {
	return graph[node].(map[string]any)["inputs"].(map[string]any)[key]
}

func TestBuildAppliesParamsWithoutMutatingTemplate(t *testing.T) {
	tpl := loadScene(t)
	graph, err := tpl.Build(map[string]any{"prompt": "a fox", "seed": 42, "lora_name": "hero.safetensors", "lora_strength": 0.7})
	if err != nil {
		t.Fatal(err)
	}
	if input(graph, "4", "text") != "a fox" || input(graph, "8", "seed") != 42 || input(graph, "11", "lora_name") != "hero.safetensors" {
		t.Fatal("params were not applied")
	}
	if input(tpl.Graph, "4", "text") != "" {
		t.Fatal("Build must not mutate the shared template")
	}
}

func TestBuildBypassesOptionalLoraNode(t *testing.T) {
	tpl := loadScene(t)
	graph, err := tpl.Build(map[string]any{"prompt": "a fox", "lora_strength": 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := graph["11"]; ok {
		t.Fatal("the LoRA node must be removed when lora_name is not given")
	}
	if got := input(graph, "7", "model"); !reflect.DeepEqual(got, []any{"1", float64(0)}) {
		t.Fatalf("model sampling must be rewired to the unet loader, got %v", got)
	}
}

func TestBuildRejectsUnknownAndMissingParams(t *testing.T) {
	tpl := loadScene(t)
	if _, err := tpl.Build(map[string]any{"prompt": "x", "cfg": 3}); !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("unknown param must be a validation error, got %v", err)
	}
	if _, err := tpl.Build(map[string]any{"seed": 1}); !errors.Is(err, pipeline.ErrValidation) {
		t.Fatalf("missing required param must be a validation error, got %v", err)
	}
}

func TestReferencedFilesListsLoaderInputs(t *testing.T) {
	tpl := loadScene(t)
	if got := tpl.ReferencedFiles(); !reflect.DeepEqual(got, []string{"te.safetensors", "unet.safetensors"}) {
		t.Fatalf("ReferencedFiles = %v", got)
	}
}

func TestLoadTemplatesRejectsParamsForMissingNode(t *testing.T) {
	fsys := fstest.MapFS{
		"wf/bad.json":        {Data: []byte(`{"1": {"class_type": "SaveImage", "inputs": {}}}`)},
		"wf/bad.params.json": {Data: []byte(`{"output_node": "1", "params": {"prompt": {"node": "9", "input": "text"}}}`)},
	}
	if _, err := LoadTemplates(fsys, "wf"); err == nil {
		t.Fatal("a param targeting a missing node must be rejected at load")
	}
}
