package scenes

import "loomtale/api/internal/models"

// Manifest tasks the storyboard uses.
const (
	taskScene     = "scene"
	taskCharsheet = "charsheet"
)

// SceneWorkflows maps each scene model of the manifest to its first
// (txt2img) workflow.
func SceneWorkflows(m *models.Manifest) func(model string) (string, bool) {
	out := map[string]string{}
	for _, e := range m.Models {
		if e.Task == taskScene && e.Engine == comfyBackend && len(e.Workflows) > 0 {
			out[e.Name] = e.Workflows[0]
		}
	}
	return func(model string) (string, bool) {
		w, ok := out[model]
		return w, ok
	}
}

// IsSceneModel reports whether the manifest has a scene model named name.
func IsSceneModel(m *models.Manifest) func(name string) bool {
	return func(name string) bool {
		e, ok := m.Get(name)
		return ok && e.Task == taskScene
	}
}

// SheetModel returns the manifest's character sheet model and workflow.
func SheetModel(m *models.Manifest) (model, workflow string) {
	for _, e := range m.Models {
		if e.Task == taskCharsheet && len(e.Workflows) > 0 {
			return e.Name, e.Workflows[0]
		}
	}
	return "", ""
}
