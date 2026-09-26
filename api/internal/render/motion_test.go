package render

import (
	"errors"
	"strings"
	"testing"
)

func testSettings() Settings {
	s := DefaultSettings()
	s.Encoder = EncoderX264
	return s
}

func TestStaticBodyGraphGolden(t *testing.T) {
	g, err := BodyGraph(testSettings(), SceneMotion{Motion: MotionStatic, Frames: 90}, Segment{Kind: SegmentBody, Frames: 81}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.String()
	if err != nil {
		t.Fatal(err)
	}
	want := "[0:v]scale=w=1920:h=1080:force_original_aspect_ratio=increase,crop=w=1920:h=1080,fps=fps=30," +
		"setsar=sar=1,format=pix_fmts=yuv420p[v]"
	if got != want {
		t.Fatalf("graph =\n%s\nwant\n%s", got, want)
	}
}

func TestKenBurnsExpressionsGolden(t *testing.T) {
	// Seed 2: even, so it pushes in; pan index 1 moves left to right.
	z, x, y := kenBurnsExprs(SceneMotion{Motion: MotionKenBurns, Seed: 2, Frames: 91}, 18)
	p := "clip((on+18)/90,0,1)"
	ease := "(" + p + "*" + p + "*(3-2*" + p + "))"
	if want := "(1.05+(0.15)*" + ease + ")"; z != want {
		t.Fatalf("z =\n%s\nwant\n%s", z, want)
	}
	if want := "(iw-iw/zoom)*(0.25+(0.5)*" + ease + ")"; x != want {
		t.Fatalf("x =\n%s\nwant\n%s", x, want)
	}
	if want := "(ih-ih/zoom)*(0.5+(0)*" + ease + ")"; y != want {
		t.Fatalf("y =\n%s\nwant\n%s", y, want)
	}
	// Odd seeds pull out, and the incoming scene of a transition counts
	// from before its own start.
	z, _, _ = kenBurnsExprs(SceneMotion{Motion: MotionKenBurns, Seed: 1, Frames: 10}, -9)
	if !strings.HasPrefix(z, "(1.2+(-0.15)*(clip((on-9)/9,0,1)") {
		t.Fatalf("pull-out z = %s", z)
	}
}

func TestKenBurnsBodyGraphPreScalesAndBurns(t *testing.T) {
	g, err := BodyGraph(testSettings(), SceneMotion{Motion: MotionKenBurns, Seed: 7, Frames: 120}, Segment{Kind: SegmentBody, Frames: 100, LocalStart: 9},
		&Burn{File: "body.ass", FontsDir: "/usr/share/fonts/loomtale"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.String()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"[0:v]scale=w=3840:h=2160:force_original_aspect_ratio=increase,crop=w=3840:h=2160,zoompan=z='",
		":d=1:s=1920x1080:fps=30,setsar=sar=1,format=pix_fmts=yuv420p,ass=filename=body.ass:fontsdir=/usr/share/fonts/loomtale[v]",
		"on+9",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("graph lacks %q:\n%s", part, got)
		}
	}
}

func TestTransitionGraphBlendsTwoMovingScenes(t *testing.T) {
	seg := Segment{Kind: SegmentTransition, Scene: 0, Frames: 18, LocalStart: 81, NextLocalStart: -9}
	g, err := TransitionGraph(testSettings(), SceneMotion{Motion: MotionStatic, Frames: 90}, SceneMotion{Motion: MotionKenBurns, Seed: 3, Frames: 60}, seg, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.String()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"format=pix_fmts=yuv420p,trim=end_frame=18[from];[1:v]",
		"on-9",
		"trim=end_frame=18[to];[from][to]xfade=transition=fade:duration=0.6:offset=0[v]",
	} {
		if !strings.Contains(got, part) {
			t.Fatalf("graph lacks %q:\n%s", part, got)
		}
	}
}

func TestParallaxIsGatedOnDepth(t *testing.T) {
	_, err := BodyGraph(testSettings(), SceneMotion{Motion: MotionParallax, Frames: 30}, Segment{Frames: 30}, nil)
	if !errors.Is(err, ErrParallaxUnavailable) {
		t.Fatalf("parallax err = %v", err)
	}
	if ok, reason := MotionAvailable(MotionParallax, false); ok || reason != "depth model not installed" {
		t.Fatalf("parallax availability = %v %q", ok, reason)
	}
	if ok, _ := MotionAvailable(MotionKenBurns, false); !ok {
		t.Fatal("ken burns needs no depth model")
	}
}

func TestSceneSeedIsStable(t *testing.T) {
	if SceneSeed("a") != 0xe40c292c || SceneSeed("a") == SceneSeed("b") {
		t.Fatal("scene seeds must be stable per id and differ between ids")
	}
}
