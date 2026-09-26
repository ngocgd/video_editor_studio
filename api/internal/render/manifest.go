package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// formatVersion is part of every hash. Bump it when the meaning of a
// hashed input or the way a segment is produced changes, so old cache
// entries stop matching instead of being reused wrongly.
const formatVersion = 1

// ManifestScene is one scene as frozen into a render manifest: the
// exact takes (by asset id and content hash) the render will use.
type ManifestScene struct {
	SceneID      string `json:"sceneId"`
	Idx          int    `json:"idx"`
	ImageAssetID string `json:"imageAssetId,omitempty"`
	ImageSha256  string `json:"imageSha256,omitempty"`
	VoiceAssetID string `json:"voiceAssetId,omitempty"`
	VoiceSha256  string `json:"voiceSha256,omitempty"`
	AlignAssetID string `json:"alignAssetId,omitempty"`
	Motion       string `json:"motion"`
	// DurationFrames is the scene's length on the timeline, taken from
	// its voice take's duration.
	DurationFrames int `json:"durationFrames"`
	// Placeholder marks a scene rendered without its own image; QC
	// reports any as a failure.
	Placeholder bool `json:"placeholder,omitempty"`
	// ImageScore is the selected image take's score when the scoring
	// step set one; QC reports it and no segment depends on it.
	ImageScore *float64 `json:"imageScore,omitempty"`
}

// imageKey identifies the image content: its sha256 when known, else
// its asset id (assets are immutable, so the id is a stable stand-in).
func (s ManifestScene) imageKey() string { return contentKey(s.ImageSha256, s.ImageAssetID) }

func (s ManifestScene) voiceKey() string { return contentKey(s.VoiceSha256, s.VoiceAssetID) }

func contentKey(sha, assetID string) string {
	if sha != "" {
		return "sha256:" + sha
	}
	if assetID != "" {
		return "asset:" + assetID
	}
	return "none"
}

// SceneMotion returns the scene's motion clock for the timeline.
func (s ManifestScene) SceneMotion() SceneMotion {
	return SceneMotion{Motion: s.Motion, Seed: SceneSeed(s.SceneID), Frames: s.DurationFrames}
}

// Manifest is a frozen render input: resolved settings plus scenes in
// order.
type Manifest struct {
	Settings Settings        `json:"settings"`
	Scenes   []ManifestScene `json:"scenes"`
}

// hashOf is the hex sha256 of v's JSON encoding. encoding/json writes
// struct fields in declaration order, so the encoding of these fixed
// structs is deterministic.
func hashOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Only plain structs of strings and numbers are hashed here.
		panic("render: unhashable value: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SettingsHash identifies the settings snapshot.
func (m Manifest) SettingsHash() string {
	return hashOf(struct {
		V int      `json:"v"`
		S Settings `json:"s"`
	}{formatVersion, m.Settings})
}

// Hash identifies the whole manifest; it is also the compose input hash,
// so two manifests with equal hashes produce the same episode file.
func (m Manifest) Hash() string {
	return hashOf(struct {
		V      int             `json:"v"`
		S      Settings        `json:"s"`
		Scenes []ManifestScene `json:"scenes"`
	}{formatVersion, m.Settings, m.Scenes})
}

// Timeline plans the manifest's frame layout.
func (m Manifest) Timeline() (Timeline, error) {
	frames := make([]int, len(m.Scenes))
	for i, s := range m.Scenes {
		frames[i] = s.DurationFrames
	}
	return PlanTimeline(m.Settings.FPS, frames, m.Settings.CrossfadeFrames())
}

// videoKey is the part of the settings every picture segment depends on.
type videoKey struct {
	V       int    `json:"v"`
	Profile string `json:"profile"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	FPS     int    `json:"fps"`
	Encoder string `json:"encoder"`
	// Style is set only when subtitles are burned in.
	Style *SubtitleStyle `json:"style,omitempty"`
}

func (m Manifest) videoKey() videoKey {
	k := videoKey{V: formatVersion, Profile: encoderProfile, W: m.Settings.Width, H: m.Settings.Height, FPS: m.Settings.FPS, Encoder: m.Settings.Encoder}
	if m.Settings.Burn() {
		style := m.Settings.SubtitleStyle
		k.Style = &style
	}
	return k
}

type sceneKey struct {
	Image  string      `json:"image"`
	Motion SceneMotion `json:"motion"`
	// Align is the alignment take whose cues are burned in; empty when
	// subtitles are not burned.
	Align string `json:"align,omitempty"`
}

func (m Manifest) sceneKeyOf(s ManifestScene) sceneKey {
	k := sceneKey{Image: s.imageKey(), Motion: s.SceneMotion()}
	if m.Settings.Burn() {
		k.Align = contentKey("", s.AlignAssetID)
	}
	return k
}

// SegmentHash is the cache key of timeline segment seg. It covers the
// video settings, the image and motion clock of every scene the segment
// shows, the window of those clocks it shows and, when subtitles are
// burned, the alignment takes whose cues it carries. Burned cues are
// computed on each scene's own clock (SegmentCues), so the key does not
// depend on where the scene sits in the episode.
func (m Manifest) SegmentHash(seg Segment) string {
	scenes := []sceneKey{m.sceneKeyOf(m.Scenes[seg.Scene])}
	if seg.Kind == SegmentTransition {
		scenes = append(scenes, m.sceneKeyOf(m.Scenes[seg.Scene+1]))
	}
	return hashOf(struct {
		Kind   SegmentKind `json:"kind"`
		Video  videoKey    `json:"video"`
		Scenes []sceneKey  `json:"scenes"`
		Local  [2]int      `json:"local"`
		Frames int         `json:"frames"`
	}{seg.Kind, m.videoKey(), scenes, [2]int{seg.LocalStart, seg.NextLocalStart}, seg.Frames})
}

// AudioHash is the cache key of the narration master: the voice takes,
// where they sit on the timeline and the loudness target.
func (m Manifest) AudioHash(t Timeline) string {
	type voice struct {
		Voice  string `json:"voice"`
		Start  int    `json:"start"`
		Frames int    `json:"frames"`
	}
	voices := make([]voice, len(m.Scenes))
	for i, s := range m.Scenes {
		voices[i] = voice{s.voiceKey(), t.Scenes[i].StartFrame, t.Scenes[i].Frames}
	}
	return hashOf(struct {
		V      int     `json:"v"`
		Kind   string  `json:"kind"`
		FPS    int     `json:"fps"`
		LUFS   int     `json:"lufs"`
		TP     int     `json:"tp"`
		Voices []voice `json:"voices"`
	}{formatVersion, "audio", m.Settings.FPS, m.Settings.LoudnessLUFSx10, m.Settings.TruePeakDBTPx10, voices})
}

// SubtitlesHash is the cache key of the episode subtitle file: the
// alignment takes and where their scenes start.
func (m Manifest) SubtitlesHash(t Timeline, lang string) string {
	type align struct {
		Align  string `json:"align"`
		Start  int    `json:"start"`
		Frames int    `json:"frames"`
	}
	aligns := make([]align, len(m.Scenes))
	for i, s := range m.Scenes {
		aligns[i] = align{contentKey("", s.AlignAssetID), t.Scenes[i].StartFrame, t.Scenes[i].Frames}
	}
	return hashOf(struct {
		V      int     `json:"v"`
		Kind   string  `json:"kind"`
		Lang   string  `json:"lang"`
		FPS    int     `json:"fps"`
		Aligns []align `json:"aligns"`
	}{formatVersion, "subtitles", lang, m.Settings.FPS, aligns})
}
