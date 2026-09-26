package bench

import "fmt"

// Case is one image to generate. RefFrom names an earlier case whose
// first output image becomes this case's reference image (character
// sheets are made from a generated portrait).
type Case struct {
	Name     string
	Model    string
	Workflow string
	Params   map[string]any
	RefFrom  string
}

// Suite is a named, ordered list of cases. Gate suites also check the
// RAM/VRAM go/no-go limits (see Harness.Run).
type Suite struct {
	Name  string
	Gate  bool
	Cases []Case
}

// Model names from models/manifest.yaml used by the image suites.
const (
	modelScene     = "z-image-turbo"
	modelThumbnail = "qwen-image"
	modelCharsheet = "qwen-image-edit-2511"
)

// scenePrompts are the image suite's 20 scene prompts, in the xianxia
// and wuxia settings the channel's stories use. The last three are
// character portraits, reused as character sheet references.
var scenePrompts = []string{
	"a sword immortal standing on a flying sword above a sea of clouds at dawn, jade mountains in the distance, digital painting",
	"an ancient mountain sect courtyard with white stone stairs, cherry blossoms and incense smoke, wide shot, cinematic light",
	"a young cultivator meditating in a cave lit by glowing spirit crystals, blue qi swirling around him",
	"two martial artists dueling on a rooftop in a rainy ancient city at night, lanterns reflecting on wet tiles",
	"a vast bamboo forest in morning mist, a lone traveller in a straw hat walking the path",
	"a celestial palace floating on clouds, golden roofs, cranes flying past, sunset",
	"an alchemist's workshop with a bronze pill furnace glowing red, shelves of herbs and scrolls",
	"a demonic beast with crimson eyes emerging from a dark swamp, lightning in the sky",
	"a bustling market street in an ancient Chinese town, merchants, silk banners, warm afternoon light",
	"a waterfall pouring into a jade lake surrounded by pine trees, a small pavilion on a rock",
	"an army of cultivators in formation on a snowy plain before a great battle, banners in the wind",
	"a hidden library with towering shelves of ancient scrolls and floating paper talismans",
	"a moonlit lotus pond with a wooden bridge and a white-robed woman playing a guqin",
	"a volcano crater with rivers of lava and a black iron sword stuck in the rock",
	"a tea house interior with wooden lattice windows, steam rising from cups, quiet evening",
	"a spirit fox with nine glowing tails sitting on a cliff under the full moon",
	"a heavenly tribulation: purple lightning striking a lone figure on a mountain peak",
	"portrait of a young male sword cultivator, long black hair tied up, white and blue hanfu, calm determined face, plain grey background, full body",
	"portrait of a young female alchemist, red and gold hanfu, jade hairpin, gentle smile, plain grey background, full body",
	"portrait of an old sect elder with a long white beard, dark green robes, holding a wooden staff, plain grey background, full body",
}

const charsheetPrompt = "turn this character into a character reference sheet: front view, side view and back view of the same character side by side, same outfit and colours, plain light grey background, clean line art"

const thumbnailPrompt = `YouTube thumbnail: a sword immortal on a flying sword above clouds, dramatic lighting, big bold title text "THE JADE CRANE SECT" at the top`

// Suites returns every benchmark suite by name.
func Suites() map[string]Suite {
	image := Suite{Name: "image"}
	for i, prompt := range scenePrompts {
		image.Cases = append(image.Cases, Case{
			Name: fmt.Sprintf("scene-%02d", i+1), Model: modelScene, Workflow: "scene_txt2img_zimage",
			Params: map[string]any{"prompt": prompt, "seed": 1000 + i},
		})
	}
	for i := range 3 {
		ref := fmt.Sprintf("scene-%02d", len(scenePrompts)-2+i)
		image.Cases = append(image.Cases, Case{
			Name: fmt.Sprintf("charsheet-%d", i+1), Model: modelCharsheet, Workflow: "charsheet_qwenedit",
			Params: map[string]any{"prompt": charsheetPrompt, "seed": 2000 + i}, RefFrom: ref,
		})
	}

	smoke := Suite{Name: "image-smoke", Cases: []Case{
		{Name: "scene", Model: modelScene, Workflow: "scene_txt2img_zimage", Params: map[string]any{"prompt": scenePrompts[len(scenePrompts)-3], "seed": 1}},
		{Name: "thumbnail", Model: modelThumbnail, Workflow: "thumbnail_qwenimage", Params: map[string]any{"prompt": thumbnailPrompt, "seed": 1}},
		{Name: "charsheet", Model: modelCharsheet, Workflow: "charsheet_qwenedit", Params: map[string]any{"prompt": charsheetPrompt, "seed": 1}, RefFrom: "scene"},
	}}

	// The go/no-go gate: three consecutive scene -> thumbnail ->
	// charsheet cycles, every step a residency switch.
	gate := Suite{Name: "image-gate", Gate: true}
	for c := 1; c <= 3; c++ {
		scene := fmt.Sprintf("cycle%d-scene", c)
		gate.Cases = append(gate.Cases,
			Case{Name: scene, Model: modelScene, Workflow: "scene_txt2img_zimage", Params: map[string]any{"prompt": scenePrompts[len(scenePrompts)-3+(c-1)], "seed": 3000 + c}},
			Case{Name: fmt.Sprintf("cycle%d-thumbnail", c), Model: modelThumbnail, Workflow: "thumbnail_qwenimage", Params: map[string]any{"prompt": thumbnailPrompt, "seed": 3000 + c}},
			Case{Name: fmt.Sprintf("cycle%d-charsheet", c), Model: modelCharsheet, Workflow: "charsheet_qwenedit", Params: map[string]any{"prompt": charsheetPrompt, "seed": 3000 + c}, RefFrom: scene},
		)
	}
	return map[string]Suite{image.Name: image, smoke.Name: smoke, gate.Name: gate}
}
