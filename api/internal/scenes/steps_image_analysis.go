package scenes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
)

// AnalysisInput is the step input of both analysis steps: the image take
// they analyse. Without it (an older or hand-queued step) they analyse the
// scene's selected image take.
type AnalysisInput struct {
	TakeID uuid.UUID `json:"takeId"`
}

// AnalysisSteps plans the analysis steps that follow a new image take:
// a score when the scene has characters, and a depth map, each only when
// installed(engine) reports the engine's model installed. Nothing is
// planned without an installed check, so a worker without the vision
// models never queues steps that could only fail.
func AnalysisSteps(ctx context.Context, sceneID, takeID uuid.UUID, hasCharacters bool, installed func(context.Context, string) error) []pipeline.StepSpec {
	if installed == nil {
		return nil
	}
	input, _ := json.Marshal(AnalysisInput{TakeID: takeID})
	var steps []pipeline.StepSpec
	add := func(kind, engine string) {
		if installed(ctx, engine) == nil {
			steps = append(steps, pipeline.StepSpec{ID: idconv.NewV7(), Kind: kind, ScopeKind: ScopeScene, ScopeID: sceneID, Priority: pipeline.PriorityBatch, Input: input})
		}
	}
	if hasCharacters {
		add(KindScore, ScoreEngine)
	}
	add(KindDepth, DepthEngine)
	return steps
}

// enqueueAnalysis queues the analysis of a new image take as its own run.
// A failure is logged, not fatal: the take already exists.
func enqueueAnalysis(ctx context.Context, d StepDeps, sc *pipeline.StepContext, sceneID, takeID uuid.UUID, hasCharacters bool) {
	if d.Service.Engine == nil || d.Vision == nil {
		return
	}
	steps := AnalysisSteps(ctx, sceneID, takeID, hasCharacters, d.VisionInstalled)
	if len(steps) == 0 {
		return
	}
	if _, err := d.Service.Engine.Enqueue(ctx, sc.Tenant(), pipeline.RunSpec{
		ID: idconv.NewV7(), ScopeKind: ScopeScene, ScopeID: sceneID, Kind: "scene.analyze", Steps: steps,
	}); err != nil {
		sc.Log(fmt.Sprintf("queueing image analysis failed: %v", err))
	}
}
