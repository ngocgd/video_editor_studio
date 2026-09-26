package scenes

import (
	"sort"

	"github.com/google/uuid"

	"loomtale/api/internal/pipeline"
)

// Take kinds and the step kind that produces each.
const (
	TakeImage = "image"
	TakeVoice = "voice"
	TakeAlign = "align"

	KindImage      = "image.generate"
	KindVoice      = "voice.synthesize"
	KindAlign      = "align.subtitles"
	KindSceneSplit = "llm.scene_split"

	ScopeScene   = "scene"
	ScopeEpisode = "episode"
)

// StepKindFor maps a take kind to its step kind.
var StepKindFor = map[string]string{TakeImage: KindImage, TakeVoice: KindVoice, TakeAlign: KindAlign}

// Lora is one LoRA applied by a style.
type Lora struct {
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
}

// Style is the image style a scene resolves to.
type Style struct {
	ID             uuid.UUID
	StylePrompt    string
	NegativePrompt string
	BaseModel      string
	Sampler        string
	Steps          int
	Width          int
	Height         int
	Loras          []Lora
}

// CharacterLook is what a character contributes to a scene image.
type CharacterLook struct {
	ID               uuid.UUID
	Name             string
	AppearancePrompt string
	NegativePrompt   string
	TriggerToken     string
	LoraVersion      int
	LoraFile         string
}

// Voice is a resolved voice (character or narrator) for one language.
type Voice struct {
	Engine       string
	PresetID     *uuid.UUID
	RefAssetID   *uuid.UUID
	Consented    bool
	PresetParams map[string]string
	Params       map[string]string
}

// MergedParams is the engine parameters of a voice: preset params
// overridden by the assignment's own.
func (v Voice) MergedParams() map[string]string {
	out := map[string]string{}
	for k, val := range v.PresetParams {
		out[k] = val
	}
	for k, val := range v.Params {
		out[k] = val
	}
	return out
}

// EpisodeInputs is everything an episode's scene hashes depend on besides
// the scene rows themselves, loaded once per episode (no per-scene query).
type EpisodeInputs struct {
	Lang         string
	DefaultStyle *Style
	Styles       map[uuid.UUID]Style
	Characters   map[uuid.UUID]CharacterLook
	Narrator     *Voice
	Voices       map[uuid.UUID]Voice
	GapMs        int
}

// SceneInputs is the part of a scene row the hashes depend on.
type SceneInputs struct {
	ID            uuid.UUID
	Narration     string
	Segments      []Segment
	ImagePrompt   string
	CharacterIDs  []uuid.UUID
	ImageStyleID  *uuid.UUID
	VoiceTakeID   *uuid.UUID
}

// StyleFor resolves a scene's style: its own, else the episode default.
func (in EpisodeInputs) StyleFor(s SceneInputs) *Style {
	if s.ImageStyleID != nil {
		if st, ok := in.Styles[*s.ImageStyleID]; ok {
			return &st
		}
	}
	return in.DefaultStyle
}

// VoiceFor resolves the voice of a segment speaker (nil = narrator).
func (in EpisodeInputs) VoiceFor(speaker *uuid.UUID) *Voice {
	if speaker != nil {
		if v, ok := in.Voices[*speaker]; ok {
			return &v
		}
		return nil
	}
	return in.Narrator
}

// Components are the named parts of an input hash. A take stores the
// components it was made from, so a stale take can say which part moved.
type Components map[string]string

// Hash is the input hash of a set of components.
func (c Components) Hash() string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		parts = append(parts, k, c[k])
	}
	h, _ := pipeline.HashInputs(parts...)
	return h
}

func mustHash(parts ...any) string {
	h, _ := pipeline.HashInputs(parts...)
	return h
}

// ImageComponents: the prompt, the style, the model, and the look of each
// character present (appearance, trigger, LoRA version). Narration is not
// part of it, so a narration edit never makes an image stale.
func ImageComponents(in EpisodeInputs, s SceneInputs) Components {
	c := Components{"prompt": mustHash(s.ImagePrompt)}
	if st := in.StyleFor(s); st != nil {
		c["model"] = st.BaseModel
		c["style"] = mustHash(st.StylePrompt, st.NegativePrompt, st.Sampler, st.Steps, st.Width, st.Height, st.Loras)
	} else {
		c["model"], c["style"] = "", ""
	}
	var looks []any
	for _, id := range s.CharacterIDs {
		if l, ok := in.Characters[id]; ok {
			looks = append(looks, l.ID, l.AppearancePrompt, l.NegativePrompt, l.TriggerToken, l.LoraVersion)
		}
	}
	c["characters"] = mustHash(looks...)
	return c
}

// VoiceComponents: the segment texts and speakers, the voices used, and
// the configured gap between segments.
func VoiceComponents(in EpisodeInputs, s SceneInputs) Components {
	var text, voices []any
	for _, seg := range s.Segments {
		text = append(text, seg.SpeakerCharacterID, seg.Text)
		if v := in.VoiceFor(seg.SpeakerCharacterID); v != nil {
			voices = append(voices, seg.SpeakerCharacterID, v.Engine, v.PresetID, v.RefAssetID, v.Consented, v.MergedParams())
		} else {
			voices = append(voices, seg.SpeakerCharacterID, "unassigned")
		}
	}
	return Components{"text": mustHash(text...), "voices": mustHash(voices...), "gap": mustHash(in.GapMs), "lang": in.Lang}
}

// AlignComponents: the voice take being aligned and the narration text.
func AlignComponents(in EpisodeInputs, s SceneInputs) Components {
	take := ""
	if s.VoiceTakeID != nil {
		take = s.VoiceTakeID.String()
	}
	return Components{"voiceTake": take, "text": mustHash(SegmentsText(s.Segments)), "lang": in.Lang}
}

// ComponentsFor dispatches on the take kind.
func ComponentsFor(kind string, in EpisodeInputs, s SceneInputs) Components {
	switch kind {
	case TakeImage:
		return ImageComponents(in, s)
	case TakeVoice:
		return VoiceComponents(in, s)
	default:
		return AlignComponents(in, s)
	}
}

// staleReasons explains which component of a take's inputs changed.
var staleReasons = map[string]string{
	"prompt":     "Image prompt changed after this image was generated",
	"style":      "Image style changed after this image was generated",
	"model":      "Base model changed after this image was generated",
	"characters": "A character's appearance or LoRA changed",
	"text":       "Narration edited after this take was generated",
	"voices":     "Voice settings changed after the voice was generated",
	"gap":        "Segment gap changed after the voice was generated",
	"lang":       "Language changed",
	"voiceTake":  "The voice take changed after subtitles were aligned",
}

// StaleReason names the first changed component (in a fixed order), or a
// generic reason when the take predates component tracking.
func StaleReason(kind string, old, current Components) string {
	order := map[string][]string{
		TakeImage: {"prompt", "characters", "style", "model"},
		TakeVoice: {"text", "voices", "gap", "lang"},
		TakeAlign: {"voiceTake", "text", "lang"},
	}[kind]
	for _, k := range order {
		if old[k] != current[k] {
			return staleReasons[k]
		}
	}
	return "Inputs changed after this take was generated"
}
