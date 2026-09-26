package render

import "fmt"

// The episode timeline is counted in whole frames. Scene i owns exactly
// its narration's frames [Start, Start+Frames); narration is laid end to
// end on the same grid, so picture and sound never drift. A crossfade
// between scene i and i+1 borrows Tail frames from the end of i and Head
// frames from the start of i+1 and is encoded as its own segment; the
// rest of each scene is its body segment. Editing scene i therefore
// touches body i and the two transitions around it, nothing else.

// SceneSpan is one scene's place on the timeline.
type SceneSpan struct {
	StartFrame int `json:"startFrame"`
	Frames     int `json:"frames"`
	// Head/Tail are the frames lent to the transitions before and after.
	Head int `json:"head"`
	Tail int `json:"tail"`
}

// SegmentKind is a video segment's role.
type SegmentKind string

const (
	SegmentBody       SegmentKind = "body"
	SegmentTransition SegmentKind = "transition"
)

// Segment is one independently encoded piece of the picture track, in
// timeline order.
type Segment struct {
	Kind SegmentKind `json:"kind"`
	// Scene is the body's scene, or the outgoing scene of a transition
	// (the incoming one is Scene+1).
	Scene      int `json:"scene"`
	StartFrame int `json:"startFrame"`
	Frames     int `json:"frames"`
	// LocalStart is the first frame of Scene's own motion clock this
	// segment shows; NextLocalStart is the incoming scene's (negative:
	// its clock starts inside the transition, held on its first frame).
	LocalStart     int `json:"localStart"`
	NextLocalStart int `json:"nextLocalStart,omitempty"`
}

// Timeline is the frame-exact layout of an episode, shared by the
// segment encoders, the audio master, subtitles and publishing.
type Timeline struct {
	FPS         int         `json:"fps"`
	Scenes      []SceneSpan `json:"scenes"`
	Segments    []Segment   `json:"segments"`
	TotalFrames int         `json:"totalFrames"`
}

// FramesForMs converts a duration to the nearest whole frame count.
func FramesForMs(ms int64, fps int) int {
	if ms <= 0 || fps <= 0 {
		return 0
	}
	return int((ms*int64(fps) + 500) / 1000)
}

// FrameMs is the time of frame f in milliseconds, rounded.
func FrameMs(f, fps int) int64 {
	if fps <= 0 {
		return 0
	}
	return (int64(f)*1000 + int64(fps)/2) / int64(fps)
}

// PlanTimeline lays out scenes of the given frame counts with crossfades
// of crossfade frames. A crossfade next to a short scene shrinks so each
// scene keeps at least one body frame; below two frames it becomes a cut.
func PlanTimeline(fps int, sceneFrames []int, crossfade int) (Timeline, error) {
	if fps <= 0 {
		return Timeline{}, fmt.Errorf("render: fps must be positive")
	}
	if len(sceneFrames) == 0 {
		return Timeline{}, fmt.Errorf("render: an episode needs at least one scene")
	}
	t := Timeline{FPS: fps, Scenes: make([]SceneSpan, len(sceneFrames))}
	for i, n := range sceneFrames {
		if n <= 0 {
			return Timeline{}, fmt.Errorf("render: scene %d has no frames", i+1)
		}
		t.Scenes[i] = SceneSpan{StartFrame: t.TotalFrames, Frames: n}
		t.TotalFrames += n
	}
	crossfade = max(crossfade, 0)
	for i := 0; i+1 < len(t.Scenes); i++ {
		// Each scene lends at most half of its frames minus one to each
		// side, so head+tail always leaves a body frame.
		room := min(lendable(t.Scenes[i].Frames), lendable(t.Scenes[i+1].Frames))
		tail, head := crossfade/2, crossfade-crossfade/2
		if tail > room || head > room {
			tail, head = room, room
		}
		if tail+head < 2 {
			tail, head = 0, 0
		}
		t.Scenes[i].Tail, t.Scenes[i+1].Head = tail, head
	}
	for i, s := range t.Scenes {
		t.Segments = append(t.Segments, Segment{
			Kind: SegmentBody, Scene: i, StartFrame: s.StartFrame + s.Head,
			Frames: s.Frames - s.Head - s.Tail, LocalStart: s.Head,
		})
		if i+1 < len(t.Scenes) && s.Tail > 0 {
			next := t.Scenes[i+1]
			t.Segments = append(t.Segments, Segment{
				Kind: SegmentTransition, Scene: i, StartFrame: s.StartFrame + s.Frames - s.Tail,
				Frames: s.Tail + next.Head, LocalStart: s.Frames - s.Tail, NextLocalStart: -next.Head,
			})
		}
	}
	return t, nil
}

func lendable(frames int) int { return (frames - 1) / 2 }

// SceneStartMs is where scene i starts, in milliseconds.
func (t Timeline) SceneStartMs(i int) int64 { return FrameMs(t.Scenes[i].StartFrame, t.FPS) }

// DurationMs is the whole episode's length in milliseconds.
func (t Timeline) DurationMs() int64 { return FrameMs(t.TotalFrames, t.FPS) }

// SegmentsOfScene returns the indexes (into Segments) of every segment
// that shows scene i: its body and the transitions on either side.
func (t Timeline) SegmentsOfScene(i int) []int {
	var out []int
	for k, s := range t.Segments {
		if s.Scene == i || (s.Kind == SegmentTransition && s.Scene+1 == i) {
			out = append(out, k)
		}
	}
	return out
}
