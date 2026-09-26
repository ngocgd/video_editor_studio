package render

import (
	"slices"
	"testing"
)

func TestPlanTimelineIsFrameExact(t *testing.T) {
	tl, err := PlanTimeline(30, []int{90, 60, 120}, 18)
	if err != nil {
		t.Fatal(err)
	}
	if tl.TotalFrames != 270 {
		t.Fatalf("total = %d", tl.TotalFrames)
	}
	want := []Segment{
		{Kind: SegmentBody, Scene: 0, StartFrame: 0, Frames: 81, LocalStart: 0},
		{Kind: SegmentTransition, Scene: 0, StartFrame: 81, Frames: 18, LocalStart: 81, NextLocalStart: -9},
		{Kind: SegmentBody, Scene: 1, StartFrame: 99, Frames: 42, LocalStart: 9},
		{Kind: SegmentTransition, Scene: 1, StartFrame: 141, Frames: 18, LocalStart: 51, NextLocalStart: -9},
		{Kind: SegmentBody, Scene: 2, StartFrame: 159, Frames: 111, LocalStart: 9},
	}
	if !slices.Equal(tl.Segments, want) {
		t.Fatalf("segments =\n%+v\nwant\n%+v", tl.Segments, want)
	}
	// Segments tile the timeline with no gap and no overlap.
	next := 0
	for _, s := range tl.Segments {
		if s.StartFrame != next || s.Frames <= 0 {
			t.Fatalf("segment %+v does not start at %d", s, next)
		}
		next += s.Frames
	}
	if next != tl.TotalFrames {
		t.Fatalf("segments cover %d of %d frames", next, tl.TotalFrames)
	}
}

func TestPlanTimelineShrinksCrossfadesAroundShortScenes(t *testing.T) {
	tl, err := PlanTimeline(30, []int{90, 5, 90, 2, 90}, 18)
	if err != nil {
		t.Fatal(err)
	}
	// A 5-frame scene lends at most 2 frames to each side; a 2-frame
	// scene cannot lend any, so its neighbours cut to it.
	if tl.Scenes[0].Tail != 2 || tl.Scenes[1].Head != 2 || tl.Scenes[1].Tail != 2 || tl.Scenes[2].Head != 2 {
		t.Fatalf("short scene crossfades = %+v", tl.Scenes)
	}
	if tl.Scenes[2].Tail != 0 || tl.Scenes[3].Head != 0 || tl.Scenes[3].Tail != 0 {
		t.Fatalf("cut around a 2-frame scene = %+v", tl.Scenes)
	}
	for _, s := range tl.Segments {
		if s.Frames <= 0 {
			t.Fatalf("empty segment %+v", s)
		}
	}
}

func TestPlanTimelineWithoutCrossfadeIsBodiesOnly(t *testing.T) {
	tl, err := PlanTimeline(25, []int{10, 10}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Segments) != 2 || tl.Segments[1].StartFrame != 10 {
		t.Fatalf("segments = %+v", tl.Segments)
	}
	if _, err := PlanTimeline(30, []int{10, 0}, 18); err == nil {
		t.Fatal("a scene without frames must be refused")
	}
	if _, err := PlanTimeline(30, nil, 18); err == nil {
		t.Fatal("an empty episode must be refused")
	}
}

func TestSegmentsOfSceneAreItsBodyAndBothTransitions(t *testing.T) {
	tl, err := PlanTimeline(30, []int{90, 90, 90, 90, 90, 90}, 18)
	if err != nil {
		t.Fatal(err)
	}
	got := tl.SegmentsOfScene(4)
	if len(got) != 3 {
		t.Fatalf("scene 4 segments = %v", got)
	}
	kinds := []SegmentKind{tl.Segments[got[0]].Kind, tl.Segments[got[1]].Kind, tl.Segments[got[2]].Kind}
	if !slices.Equal(kinds, []SegmentKind{SegmentTransition, SegmentBody, SegmentTransition}) {
		t.Fatalf("scene 4 segment kinds = %v", kinds)
	}
}

func TestFrameMath(t *testing.T) {
	if FramesForMs(600, 30) != 18 || FramesForMs(1016, 30) != 30 || FramesForMs(1017, 30) != 31 {
		t.Fatal("FramesForMs rounds to the nearest frame")
	}
	if FrameMs(1, 30) != 33 || FrameMs(2, 30) != 67 || FrameMs(270, 30) != 9000 {
		t.Fatal("FrameMs rounds to the nearest millisecond")
	}
	for _, fps := range []int{24, 25, 30, 60} {
		if SamplesForFrames(fps, fps) != AudioSampleRate {
			t.Fatalf("one second at %d fps is not %d samples", fps, AudioSampleRate)
		}
	}
}
