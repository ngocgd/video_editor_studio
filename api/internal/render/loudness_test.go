package render

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const loudnormLog = `[Parsed_loudnorm_4 @ 0x5581]
{
	"input_i" : "-23.54",
	"input_tp" : "-7.12",
	"input_lra" : "5.60",
	"input_thresh" : "-33.97",
	"output_i" : "-14.02",
	"output_tp" : "-1.00",
	"output_lra" : "4.90",
	"output_thresh" : "-24.40",
	"normalization_type" : "dynamic",
	"target_offset" : "0.02"
}
`

func TestParseLoudnormAndSecondPassGraph(t *testing.T) {
	st, err := ParseLoudnorm("Input #0, wav ...\n" + loudnormLog)
	if err != nil {
		t.Fatal(err)
	}
	if st.InputI != "-23.54" || st.TargetOffset != "0.02" {
		t.Fatalf("stats = %+v", st)
	}
	tl, _ := PlanTimeline(30, []int{30, 45}, 18)
	g, err := AudioGraph(DefaultSettings(), tl, &st)
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.String()
	if err != nil {
		t.Fatal(err)
	}
	want := "[0:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=mono,apad=whole_len=48000,atrim=end_sample=48000,asetpts=expr='PTS-STARTPTS'[s0];" +
		"[1:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=mono,apad=whole_len=72000,atrim=end_sample=72000,asetpts=expr='PTS-STARTPTS'[s1];" +
		"[s0][s1]concat=n=2:v=0:a=1,loudnorm=i=-14:tp=-1:lra=11:measured_i=-23.54:measured_tp=-7.12:measured_lra=5.6:measured_thresh=-33.97:offset=0.02:linear=true," +
		"aresample=osr=48000[a]"
	if got != want {
		t.Fatalf("graph =\n%s\nwant\n%s", got, want)
	}
	first, _ := AudioGraph(DefaultSettings(), tl, nil)
	s, _ := first.String()
	if !strings.Contains(s, "loudnorm=i=-14:tp=-1:lra=11:print_format=json[a]") {
		t.Fatalf("first pass = %s", s)
	}
}

func TestParseLoudnormRefusesSilence(t *testing.T) {
	silent := strings.Replace(loudnormLog, `"-23.54"`, `"-inf"`, 1)
	if _, err := ParseLoudnorm(silent); err == nil {
		t.Fatal("a silent master must not be normalised")
	}
	if _, err := ParseLoudnorm("no json here"); err == nil {
		t.Fatal("a log without a measurement must fail")
	}
}

func TestParseEBUR128Summary(t *testing.T) {
	log := `[Parsed_ebur128_0 @ 0x1] t: 9.9 M: -14.2 S: -14.0 I: -14.1 LUFS
[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -14.1 LUFS
    Threshold: -24.5 LUFS

  Loudness range:
    LRA:         4.1 LU

  True peak:
    Peak:       -1.3 dBFS
`
	l, err := ParseEBUR128(log)
	if err != nil {
		t.Fatal(err)
	}
	if l.IntegratedLUFS != -14.1 || l.TruePeakDBTP != -1.3 {
		t.Fatalf("loudness = %+v", l)
	}
	if _, err := ParseEBUR128("Summary:\n I: -14 LUFS\n"); err == nil {
		t.Fatal("a summary without a true peak must fail")
	}
}

type fakeProber struct {
	err   error
	calls int
}

func (f *fakeProber) ProbeEncoder(context.Context, string) error {
	f.calls++
	return f.err
}

func TestEncoderProbeFallsBackAndCaches(t *testing.T) {
	p := &fakeProber{err: errors.New("no nvenc")}
	probe := &EncoderProbe{Prober: p}
	for range 3 {
		if info := probe.Info(context.Background()); info.Codec != EncoderX264 || info.NVENC || info.Reason == "" {
			t.Fatalf("info = %+v", info)
		}
	}
	if p.calls != 1 {
		t.Fatalf("probed %d times, want once", p.calls)
	}
	ok := &EncoderProbe{Prober: &fakeProber{}}
	if info := ok.Info(context.Background()); info.Codec != EncoderNVENC || !info.NVENC {
		t.Fatalf("info = %+v", info)
	}
}

func TestResolveEncoder(t *testing.T) {
	noGPU := EncoderInfo{Codec: EncoderX264}
	gpu := EncoderInfo{Codec: EncoderNVENC, NVENC: true}
	cases := []struct {
		choice string
		info   EncoderInfo
		want   string
		fails  bool
	}{
		{EncoderAuto, noGPU, EncoderX264, false},
		{EncoderAuto, gpu, EncoderNVENC, false},
		{EncoderAuto, EncoderInfo{}, EncoderX264, false},
		{EncoderNVENC, noGPU, "", true},
		{EncoderNVENC, gpu, EncoderNVENC, false},
		{EncoderX264, gpu, EncoderX264, false},
		{"libx265", gpu, "", true},
	}
	for _, c := range cases {
		got, err := ResolveEncoder(c.choice, c.info)
		if got != c.want || (err != nil) != c.fails {
			t.Fatalf("ResolveEncoder(%q, %+v) = %q, %v", c.choice, c.info, got, err)
		}
	}
	if v := VideoEncode(EncoderX264, 30); v.GOP != 60 || !v.ClosedSegment || v.TimeScale != 15360 || v.Preset != "veryfast" {
		t.Fatalf("x264 encode = %+v", v)
	}
	if v := VideoEncode(EncoderNVENC, 30); v.Preset != "p4" {
		t.Fatalf("nvenc encode = %+v", v)
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatal(err)
	}
	if DefaultSettings().CrossfadeFrames() != 18 {
		t.Fatal("0.6s at 30fps is 18 frames")
	}
	bad := []func(s *Settings){
		func(s *Settings) { s.Width = 1921 },
		func(s *Settings) { s.FPS = 29 },
		func(s *Settings) { s.Encoder = "libx265" },
		func(s *Settings) { s.SubtitleStyle.Font = "Lit,erata" },
		func(s *Settings) { s.SubtitleStyle.Position = "left" },
		func(s *Settings) { s.LoudnessLUFSx10 = 0 },
		func(s *Settings) { s.DefaultMotion = "zoom" },
	}
	for i, mutate := range bad {
		s := DefaultSettings()
		mutate(&s)
		if s.Validate() == nil {
			t.Fatalf("case %d should be invalid", i)
		}
	}
}
