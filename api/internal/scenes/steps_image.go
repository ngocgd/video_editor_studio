package scenes

import (
	"context"
	"fmt"
	_ "image/png" // DecodeConfig of generated PNGs
	"strings"

	"loomtale/api/internal/pipeline"
)

// comfyBackend is the residency backend name of ComfyUI models.
const comfyBackend = "comfyui"

// ImageHandler is image.generate: the scene's style, characters and
// prompt become a ComfyUI workflow run; the image becomes a take.
type ImageHandler struct{ StepDeps }

var _ pipeline.StepHandler = (*ImageHandler)(nil)

func (h *ImageHandler) Kind() string { return KindImage }

func (h *ImageHandler) Queue(context.Context, pipeline.StepRef) (string, error) {
	return pipeline.QueueGPU, nil
}

func (h *ImageHandler) InputHash(ctx context.Context, s pipeline.StepRef) (string, error) {
	sc, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return "", err
	}
	return ImageComponents(sc.Inputs, sc.sceneInputs()).Hash(), nil
}

// ModelRef is the style's base model on ComfyUI; nil without a style
// (Run then fails with a clear validation error instead).
func (h *ImageHandler) ModelRef(ctx context.Context, s pipeline.StepRef) (*pipeline.ModelRef, error) {
	sc, err := stepContext(ctx, h.StepDeps, s)
	if err != nil {
		return nil, nil //nolint:nilerr // Run reports the real error
	}
	if st := sc.Inputs.StyleFor(sc.sceneInputs()); st != nil && st.BaseModel != "" {
		return &pipeline.ModelRef{Backend: comfyBackend, Model: st.BaseModel}, nil
	}
	return nil, nil
}

// ScenePrompt is the positive prompt: the style prompt, then each present
// character's trigger token and appearance, then the scene's own prompt.
// Every part is sent as a JSON string value in the workflow graph, never
// spliced into the graph's structure.
func ScenePrompt(st *Style, looks []CharacterLook, scenePrompt string) string {
	var parts []string
	if st != nil && strings.TrimSpace(st.StylePrompt) != "" {
		parts = append(parts, strings.TrimSpace(st.StylePrompt))
	}
	for _, l := range looks {
		var p []string
		if l.TriggerToken != "" {
			p = append(p, l.TriggerToken)
		}
		if a := strings.TrimSpace(l.AppearancePrompt); a != "" {
			p = append(p, a)
		}
		if len(p) > 0 {
			parts = append(parts, strings.Join(p, ", "))
		}
	}
	if sp := strings.TrimSpace(scenePrompt); sp != "" {
		parts = append(parts, sp)
	}
	return strings.Join(parts, ", ")
}

func (h *ImageHandler) Run(ctx context.Context, sc *pipeline.StepContext) (pipeline.Output, error) {
	scx, err := loadSceneContext(ctx, h.Service.Queries, sc.Tenant(), sc.ScopeID())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", pipeline.ErrValidation, err)
	}
	in := scx.sceneInputs()
	st := scx.Inputs.StyleFor(in)
	if st == nil {
		return nil, fmt.Errorf("%w: no image style: create one in Settings > Styles", pipeline.ErrValidation)
	}
	if h.Comfy == nil || h.SceneWorkflow == nil || h.Storage == nil {
		return nil, fmt.Errorf("%w: the image engine is not available on this worker", pipeline.ErrEngineNotInstalled)
	}
	workflow, ok := h.SceneWorkflow(st.BaseModel)
	if !ok {
		return nil, fmt.Errorf("%w: model %q has no scene workflow", pipeline.ErrEngineNotInstalled, st.BaseModel)
	}
	tpl, ok := h.Comfy.Templates[workflow]
	if !ok {
		return nil, fmt.Errorf("%w: workflow %q is missing", pipeline.ErrEngineNotInstalled, workflow)
	}

	var looks []CharacterLook
	for _, id := range in.CharacterIDs {
		if l, ok := scx.Inputs.Characters[id]; ok {
			looks = append(looks, l)
		}
	}
	seed := newSeed()
	params := map[string]any{
		"prompt": ScenePrompt(st, looks, in.ImagePrompt), "seed": seed, "steps": st.Steps,
		"width": st.Width, "height": st.Height, "filename_prefix": "loomtale",
	}
	lora, strength := "", 1.0
	for _, l := range looks {
		if l.LoraFile != "" {
			lora = l.LoraFile
			break
		}
	}
	if lora == "" && len(st.Loras) > 0 {
		lora, strength = st.Loras[0].Name, st.Loras[0].Strength
	}
	if lora != "" {
		params["lora_name"], params["lora_strength"] = lora, strength
	}
	// Only the parameters this workflow declares: Build refuses unknown ones.
	for name := range params {
		if _, ok := tpl.Map.Params[name]; !ok {
			delete(params, name)
		}
	}

	sc.Progress(5, 0)
	res, err := h.Comfy.Run(ctx, workflow, params, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Images) == 0 {
		return nil, fmt.Errorf("scenes: workflow %s returned no image", workflow)
	}
	asset, err := storeDerived(ctx, h.StepDeps, sc.Tenant(), "image", "image/png", res.Images[0], 0)
	if err != nil {
		return nil, err
	}
	components := ImageComponents(scx.Inputs, in)
	take, err := h.Service.RecordTake(ctx, sc.Tenant(), in.ID, TakeImage, idOf(asset.ID), components,
		map[string]any{"seed": seed, "model": st.BaseModel, "workflow": workflow, "seconds": res.Seconds, "lora": lora}, sc.StepID(), sc.RunID())
	if err != nil {
		return nil, err
	}
	enqueueDerivatives(ctx, h.StepDeps, sc, asset)
	return pipeline.Output{"assetId": idOf(asset.ID).String(), "takeId": idOf(take.ID).String(), "seed": seed, "seconds": res.Seconds}, nil
}
