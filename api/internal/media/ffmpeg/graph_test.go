package ffmpeg

import (
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGraphRendersQuotedExpressionsAndLabels(t *testing.T) {
	g := Graph{Chains: []Chain{
		{In: []string{"0:v"}, Filters: []Filter{
			F("scale", O("w", Int(3840)), O("h", Int(2160))),
			F("zoompan", O("z", Expr("1+0.1*min(1,on/59)")), O("d", Int(1)), O("s", Size(1920, 1080)), O("fps", Int(30))),
			F("ass", O("filename", File("seg.ass")), O("fontsdir", Dir("/usr/share/fonts/loomtale"))),
		}, Out: []string{"v"}},
		{In: []string{"1:a"}, Filters: []Filter{F("anull")}, Out: []string{"a"}},
	}}
	got, err := g.String()
	if err != nil {
		t.Fatal(err)
	}
	want := "[0:v]scale=w=3840:h=2160,zoompan=z='1+0.1*min(1,on/59)':d=1:s=1920x1080:fps=30," +
		"ass=filename=seg.ass:fontsdir=/usr/share/fonts/loomtale[v];[1:a]anull[a]"
	if got != want {
		t.Fatalf("graph =\n%s\nwant\n%s", got, want)
	}
}

func TestGraphRefusesInjection(t *testing.T) {
	cases := map[string]Filter{
		"quote in expression":     F("zoompan", O("z", Expr("1'[x];movie=/etc/passwd"))),
		"colon in expression":     F("zoompan", O("z", Expr("1:x=2"))),
		"file with a directory":   F("ass", O("filename", File("../x.ass"))),
		"file with a protocol":    F("ass", O("filename", File("http:x"))),
		"enum with a separator":   F("format", O("pix_fmts", Enum("yuv420p,movie"))),
		"filter not allowlisted":  F("movie", O("filename", File("x.mp4"))),
		"cuda filter":             F("scale_cuda", O("w", Int(1))),
		"bad key":                 F("scale", O("w=1:h", Int(1))),
		"infinite float":          F("setpts", O("expr", Float(math.Inf(1)))),
		"relative fonts dir":      F("ass", O("fontsdir", Dir("fonts"))),
		"fonts dir with traverse": F("ass", O("fontsdir", Dir("/usr/../etc"))),
		"zero value":              F("scale", O("w", Value{})),
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Graph{Chains: []Chain{{Filters: []Filter{f}}}}.String()
			if !IsRefused(err) {
				t.Fatalf("expected a refusal, got %v", err)
			}
		})
	}
	if _, err := (Graph{Chains: []Chain{{In: []string{"0:v];[x"}, Filters: []Filter{F("null")}}}}).String(); !IsRefused(err) {
		t.Fatalf("label injection: %v", err)
	}
}

func TestSegmentEncodeArgs(t *testing.T) {
	dir := t.TempDir()
	job := Job{
		TempDir: dir,
		Inputs:  []Input{{Format: FormatImage2, Path: filepath.Join(dir, "img.png"), Loop: true, FrameRate: 30}},
		Graph:   &Graph{Chains: []Chain{{In: []string{"0:v"}, Filters: []Filter{F("format", O("pix_fmts", Enum("yuv420p")))}, Out: []string{"v"}}}},
		Output: Output{
			Path: filepath.Join(dir, "seg.mp4"), Muxer: "mp4", VideoCodec: CodecX264, Frames: 90, NoAudio: true, Maps: []string{"v"},
			Video: &VideoEncode{Preset: "veryfast", Quality: 20, PixelFormat: "yuv420p", FrameRate: 30, GOP: 90, ClosedSegment: true, TimeScale: 15360},
		},
	}
	args, err := (&Runner{}).Args(job)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-protocol_whitelist", "file", "-f", "image2", "-loop", "1", "-framerate", "30", "-i", filepath.Join(dir, "img.png"),
		"-filter_complex", "[0:v]format=pix_fmts=yuv420p[v]",
		"-progress", "pipe:1",
		"-map", "[v]", "-an", "-c:v", "libx264",
		"-preset", "veryfast", "-profile:v", "high", "-crf", "20", "-pix_fmt", "yuv420p", "-r", "30",
		"-g", "90", "-keyint_min", "90", "-bf", "0", "-force_key_frames", "expr:eq(n,0)", "-sc_threshold", "0", "-flags", "+cgop",
		"-video_track_timescale", "15360",
		"-frames:v", "90", "-f", "mp4", filepath.Join(dir, "seg.mp4"),
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args =\n%q\nwant\n%q", args, want)
	}

	job.Output.VideoCodec = CodecNVENC
	job.Output.Video.Preset = "p4"
	args, err = (&Runner{}).Args(job)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, part := range []string{"-c:v h264_nvenc -preset p4", "-rc vbr -cq 20 -b:v 0", "-bf 0 -force_key_frames expr:eq(n,0) -forced-idr 1"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("nvenc args lack %q: %s", part, joined)
		}
	}
}

func TestComposeArgsCopyStreamsAndMuxSubtitles(t *testing.T) {
	dir := t.TempDir()
	job := Job{
		TempDir: dir,
		Inputs: []Input{
			{Format: FormatConcat, Path: filepath.Join(dir, "list.txt")},
			{Format: FormatMP4, Path: filepath.Join(dir, "audio.m4a")},
			{Format: FormatSRT, Path: filepath.Join(dir, "subs.srt")},
		},
		Output: Output{
			Path: filepath.Join(dir, "episode.mp4"), Muxer: "mp4", Maps: []string{"0:v", "1:a", "2:s"},
			VideoCodec: CodecCopy, AudioCodec: CodecCopy, SubtitleCodec: "mov_text", SubtitleLanguage: "eng", Faststart: true,
		},
	}
	args, err := (&Runner{}).Args(job)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, part := range []string{
		"-f concat -safe 1 -i", "-f srt -i",
		"-map 0:v -map 1:a -map 2:s -c:v copy -c:a copy -c:s mov_text -metadata:s:s:0 language=eng -movflags +faststart -f mp4",
	} {
		if !strings.Contains(joined, part) {
			t.Fatalf("compose args lack %q: %s", part, joined)
		}
	}
}

func TestRenderOutputRefusals(t *testing.T) {
	dir := t.TempDir()
	base := func() Job {
		return Job{
			TempDir: dir,
			Inputs:  []Input{{Format: FormatWAV, Path: filepath.Join(dir, "a.wav")}},
			Output:  Output{Muxer: MuxerNull},
		}
	}
	cases := map[string]func(j *Job){
		"null output with a path": func(j *Job) { j.Output.Path = filepath.Join(dir, "x") },
		"loop on an audio input":  func(j *Job) { j.Inputs[0].Loop = true },
		"bad map":                 func(j *Job) { j.Output.Maps = []string{"0:v;rm"} },
		"video settings on webp":  func(j *Job) { j.Output.VideoCodec = "libwebp"; j.Output.Video = &VideoEncode{} },
		"nvenc preset on x264":    func(j *Job) { j.Output.VideoCodec = CodecX264; j.Output.Video = &VideoEncode{Preset: "p4"} },
		"subtitle codec":          func(j *Job) { j.Output.SubtitleCodec = "ass" },
		"subtitle language":       func(j *Job) { j.Output.SubtitleLanguage = "en;x" },
		"faststart on null":       func(j *Job) { j.Output.Faststart = true },
		"graph plus scale": func(j *Job) {
			j.Graph = &Graph{Chains: []Chain{{Filters: []Filter{F("anull")}}}}
			j.Output.ScaleWidth = 10
		},
		"quality out of range":      func(j *Job) { j.Output.VideoCodec = CodecX264; j.Output.Video = &VideoEncode{Quality: 99} },
		"remote concat stays local": func(j *Job) { j.Inputs[0] = Input{Format: FormatConcat, URL: "https://h/x"} },
	}
	if _, err := (&Runner{}).Args(base()); err != nil {
		t.Fatalf("base job refused: %v", err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := base()
			mutate(&j)
			if _, err := (&Runner{RemoteHost: "h"}).Args(j); !IsRefused(err) {
				t.Fatalf("expected a refusal, got %v", err)
			}
		})
	}
}

func TestProbeEncoderArgsAreFixed(t *testing.T) {
	args, err := ProbeEncoderArgs(CodecNVENC)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "-hide_banner -nostdin -loglevel error -f lavfi -i color=c=black:s=256x256:r=30 -frames:v 1 -c:v h264_nvenc -f null -" {
		t.Fatalf("probe args = %s", got)
	}
	if _, err := ProbeEncoderArgs("libx265"); !IsRefused(err) {
		t.Fatalf("unknown encoder: %v", err)
	}
}

func TestProbeArgsStayInTheTempDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := ProbeArgs(dir, filepath.Join(filepath.Dir(dir), "x.mp4"), FormatMP4); !IsRefused(err) {
		t.Fatalf("outside path: %v", err)
	}
	args, err := ProbeArgs(dir, filepath.Join(dir, "x.mp4"), FormatMP4)
	if err != nil || !slices.Contains(args, "json") {
		t.Fatalf("probe args = %q, %v", args, err)
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{limit: 5}
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defgh"))
	if b.String() != "defgh" {
		t.Fatalf("tail = %q", b.String())
	}
}
