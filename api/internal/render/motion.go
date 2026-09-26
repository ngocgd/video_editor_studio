package render

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"

	"loomtale/api/internal/media/ffmpeg"
)

// ErrParallaxUnavailable is why parallax cannot be chosen yet: it needs a
// depth mask from a depth model the GPU worker does not offer.
var ErrParallaxUnavailable = errors.New("depth model not installed")

// SceneMotion is the motion of one scene's still image over the scene's
// whole length. Segments show a window of it, so a body and the
// transitions around it continue the same camera move seamlessly.
type SceneMotion struct {
	Motion string `json:"motion"`
	// Seed picks the Ken Burns direction; derived from the scene id so a
	// scene keeps its move across renders.
	Seed uint32 `json:"seed"`
	// Frames is the scene's full length on the timeline.
	Frames int `json:"frames"`
}

// SceneSeed derives a scene's motion seed from its id.
func SceneSeed(sceneID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sceneID))
	return h.Sum32()
}

// MotionAvailable says whether motion can be rendered with the given
// GPU capabilities and, when not, why.
func MotionAvailable(motion string, depth bool) (bool, string) {
	switch motion {
	case MotionStatic, MotionKenBurns:
		return true, ""
	case MotionParallax:
		if depth {
			return false, "parallax rendering is not built yet"
		}
		return false, ErrParallaxUnavailable.Error()
	default:
		return false, fmt.Sprintf("unknown motion %q", motion)
	}
}

// kenBurnsPans are the pan paths a seed chooses from, as the viewport
// centre's share of the free image area: {x0, x1, y0, y1}.
var kenBurnsPans = [][4]float64{
	{0.5, 0.5, 0.5, 0.5}, {0.25, 0.75, 0.5, 0.5}, {0.75, 0.25, 0.5, 0.5}, {0.5, 0.5, 0.25, 0.75}, {0.5, 0.5, 0.75, 0.25},
}

// Ken Burns zoom range; pushing in or pulling out is seeded per scene.
const (
	kenBurnsZoomNear = 1.05
	kenBurnsZoomFar  = 1.2
)

// motionFilters renders m for w×h at fps, starting localStart frames
// into the scene's own clock. The source is scaled to cover the frame
// (twice the size for Ken Burns, so the sub-pixel move stays smooth).
func motionFilters(m SceneMotion, w, h, fps, localStart int) ([]ffmpeg.Filter, error) {
	cover := func(cw, ch int) []ffmpeg.Filter {
		return []ffmpeg.Filter{
			ffmpeg.F("scale", ffmpeg.O("w", ffmpeg.Int(cw)), ffmpeg.O("h", ffmpeg.Int(ch)),
				ffmpeg.O("force_original_aspect_ratio", ffmpeg.Enum("increase"))),
			ffmpeg.F("crop", ffmpeg.O("w", ffmpeg.Int(cw)), ffmpeg.O("h", ffmpeg.Int(ch))),
		}
	}
	finish := []ffmpeg.Filter{
		ffmpeg.F("setsar", ffmpeg.O("sar", ffmpeg.Int(1))),
		ffmpeg.F("format", ffmpeg.O("pix_fmts", ffmpeg.Enum("yuv420p"))),
	}
	switch m.Motion {
	case MotionStatic:
		out := cover(w, h)
		out = append(out, ffmpeg.F("fps", ffmpeg.O("fps", ffmpeg.Int(fps))))
		return append(out, finish...), nil
	case MotionKenBurns:
		out := cover(2*w, 2*h)
		z, x, y := kenBurnsExprs(m, localStart)
		out = append(out, ffmpeg.F("zoompan",
			ffmpeg.O("z", ffmpeg.Expr(z)), ffmpeg.O("x", ffmpeg.Expr(x)), ffmpeg.O("y", ffmpeg.Expr(y)),
			ffmpeg.O("d", ffmpeg.Int(1)), ffmpeg.O("s", ffmpeg.Size(w, h)), ffmpeg.O("fps", ffmpeg.Int(fps))))
		return append(out, finish...), nil
	case MotionParallax:
		return nil, ErrParallaxUnavailable
	default:
		return nil, fmt.Errorf("render: unknown motion %q", m.Motion)
	}
}

// kenBurnsExprs returns the zoompan z, x and y expressions. Progress p
// runs 0→1 over the scene's frames on the scene's own clock (zoompan's
// output counter "on" plus the segment's offset) and is eased with
// smoothstep, so a move starts and ends gently and a transition picks
// it up exactly where the body left it.
func kenBurnsExprs(m SceneMotion, localStart int) (z, x, y string) {
	den := max(m.Frames-1, 1)
	clock := "on"
	if localStart > 0 {
		clock = "on+" + strconv.Itoa(localStart)
	} else if localStart < 0 {
		clock = "on-" + strconv.Itoa(-localStart)
	}
	p := "clip((" + clock + ")/" + strconv.Itoa(den) + ",0,1)"
	ease := "(" + p + "*" + p + "*(3-2*" + p + "))"
	z0, z1 := kenBurnsZoomNear, kenBurnsZoomFar
	if m.Seed&1 == 1 {
		z0, z1 = z1, z0
	}
	pan := kenBurnsPans[(m.Seed>>1)%uint32(len(kenBurnsPans))]
	lerp := func(a, b float64) string { return "(" + num(a) + "+(" + num(b-a) + ")*" + ease + ")" }
	z = lerp(z0, z1)
	x = "(iw-iw/zoom)*" + lerp(pan[0], pan[1])
	y = "(ih-ih/zoom)*" + lerp(pan[2], pan[3])
	return z, x, y
}

// num formats v with at most six decimals, so float noise such as
// 0.1499999999999999 never reaches a filter string or a golden test.
func num(v float64) string { return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64) }

// Burn names the ASS file (inside the step's temp dir) burned into a
// segment and the fonts directory baked into the worker image.
type Burn struct {
	File     string
	FontsDir string
}

func burnFilter(b *Burn) []ffmpeg.Filter {
	if b == nil {
		return nil
	}
	return []ffmpeg.Filter{ffmpeg.F("ass", ffmpeg.O("filename", ffmpeg.File(b.File)), ffmpeg.O("fontsdir", ffmpeg.Dir(b.FontsDir)))}
}

// OutputLabel is the graph label a segment's picture comes out on.
const OutputLabel = "v"

// BodyGraph is the filtergraph of a scene body segment. Input 0 is the
// scene image, looped at the render frame rate.
func BodyGraph(s Settings, m SceneMotion, seg Segment, burn *Burn) (*ffmpeg.Graph, error) {
	filters, err := motionFilters(m, s.Width, s.Height, s.FPS, seg.LocalStart)
	if err != nil {
		return nil, err
	}
	filters = append(filters, burnFilter(burn)...)
	return &ffmpeg.Graph{Chains: []ffmpeg.Chain{{In: []string{"0:v"}, Filters: filters, Out: []string{OutputLabel}}}}, nil
}

// TransitionGraph is the filtergraph of the crossfade from scene "from"
// (input 0) to scene "to" (input 1). Both keep moving on their own clocks
// while they blend.
func TransitionGraph(s Settings, from, to SceneMotion, seg Segment, burn *Burn) (*ffmpeg.Graph, error) {
	a, err := motionFilters(from, s.Width, s.Height, s.FPS, seg.LocalStart)
	if err != nil {
		return nil, err
	}
	b, err := motionFilters(to, s.Width, s.Height, s.FPS, seg.NextLocalStart)
	if err != nil {
		return nil, err
	}
	trim := ffmpeg.F("trim", ffmpeg.O("end_frame", ffmpeg.Int(seg.Frames)))
	blend := []ffmpeg.Filter{ffmpeg.F("xfade",
		ffmpeg.O("transition", ffmpeg.Enum("fade")),
		ffmpeg.O("duration", ffmpeg.Float(float64(seg.Frames)/float64(s.FPS))),
		ffmpeg.O("offset", ffmpeg.Int(0)))}
	blend = append(blend, burnFilter(burn)...)
	return &ffmpeg.Graph{Chains: []ffmpeg.Chain{
		{In: []string{"0:v"}, Filters: append(a, trim), Out: []string{"from"}},
		{In: []string{"1:v"}, Filters: append(b, trim), Out: []string{"to"}},
		{In: []string{"from", "to"}, Filters: blend, Out: []string{OutputLabel}},
	}}, nil
}
