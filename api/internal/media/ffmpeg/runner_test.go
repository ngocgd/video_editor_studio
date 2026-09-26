package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func tempJob(t *testing.T) (string, Job) {
	t.Helper()
	dir := t.TempDir()
	return dir, Job{
		TempDir: dir,
		Inputs:  []Input{{Format: FormatImage2, Path: filepath.Join(dir, "source.png")}},
		Output:  Output{Path: filepath.Join(dir, "out.webp"), Muxer: "webp", VideoCodec: "libwebp", ScaleWidth: 320, Quality: 80, Frames: 1},
	}
}

func TestArgsForcesFormatWhitelistAndQuietFlags(t *testing.T) {
	dir, job := tempJob(t)
	args, err := (&Runner{}).Args(job)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-protocol_whitelist", "file", "-f", "image2", "-i", filepath.Join(dir, "source.png"),
		"-progress", "pipe:1",
		"-vf", "scale=320:-2", "-c:v", "libwebp", "-q:v", "80", "-frames:v", "1",
		"-f", "webp", filepath.Join(dir, "out.webp"),
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args =\n%q\nwant\n%q", args, want)
	}
}

func TestArgsRemoteInputUsesTheRemoteWhitelist(t *testing.T) {
	_, job := tempJob(t)
	job.Inputs = []Input{{Format: FormatWAV, URL: "https://minio.internal:9000/loomtale/t/x/audio/y?X-Amz-Signature=abc"}}
	args, err := (&Runner{RemoteHost: "minio.internal:9000"}).Args(job)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-protocol_whitelist https,tls,tcp -f wav -i https://minio.internal:9000/") {
		t.Fatalf("remote input args = %s", joined)
	}
}

func TestArgsRefusals(t *testing.T) {
	dir, base := tempJob(t)
	outside := filepath.Join(filepath.Dir(dir), "elsewhere.png")
	cases := map[string]func(j *Job){
		"file outside the temp dir": func(j *Job) { j.Inputs[0].Path = outside },
		"parent traversal": func(j *Job) {
			j.Inputs[0].Path = dir + string(filepath.Separator) + ".." + string(filepath.Separator) + "x.png"
		},
		"the temp dir itself":         func(j *Job) { j.Inputs[0].Path = dir },
		"relative path":               func(j *Job) { j.Inputs[0].Path = "source.png" },
		"concat protocol input":       func(j *Job) { j.Inputs[0] = Input{Format: FormatImage2, URL: "concat:a.png|b.png"} },
		"file protocol url":           func(j *Job) { j.Inputs[0] = Input{Format: FormatImage2, URL: "file:///etc/passwd"} },
		"plain http":                  func(j *Job) { j.Inputs[0] = Input{Format: FormatWAV, URL: "http://minio.internal:9000/a"} },
		"foreign host":                func(j *Job) { j.Inputs[0] = Input{Format: FormatWAV, URL: "https://evil.example/a"} },
		"credentials in url":          func(j *Job) { j.Inputs[0] = Input{Format: FormatWAV, URL: "https://u:p@minio.internal:9000/a"} },
		"remote concat list":          func(j *Job) { j.Inputs[0] = Input{Format: FormatConcat, URL: "https://minio.internal:9000/list"} },
		"unforced or unknown format":  func(j *Job) { j.Inputs[0].Format = "lavfi" },
		"both path and url":           func(j *Job) { j.Inputs[0].URL = "https://minio.internal:9000/a" },
		"output outside the temp dir": func(j *Job) { j.Output.Path = outside },
		"output muxer not allowed":    func(j *Job) { j.Output.Muxer = "hls" },
		"output codec not allowed":    func(j *Job) { j.Output.VideoCodec = "libx265" },
		"no inputs":                   func(j *Job) { j.Inputs = nil },
		"relative temp dir":           func(j *Job) { j.TempDir = "tmp" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			job := base
			job.Inputs = slices.Clone(base.Inputs)
			mutate(&job)
			if _, err := (&Runner{RemoteHost: "minio.internal:9000"}).Args(job); !IsRefused(err) {
				t.Fatalf("expected a refusal, got %v", err)
			}
		})
	}
}

func TestRunRefusesBeforeStartingAProcess(t *testing.T) {
	_, job := tempJob(t)
	job.Inputs[0] = Input{Format: FormatImage2, URL: "concat:a|b"}
	// A binary that does not exist: reaching exec would fail differently.
	err := (&Runner{Binary: "/nonexistent/ffmpeg"}).Run(context.Background(), job)
	if !IsRefused(err) {
		t.Fatalf("Run = %v, want a refusal", err)
	}
}

func TestConcatListOnlyAcceptsPlainNames(t *testing.T) {
	dir := t.TempDir()
	write := func(p string, data []byte) error { return os.WriteFile(p, data, 0o600) }
	p, err := WriteConcatList(dir, "list.txt", []string{"seg-000.wav", "gap.wav", "seg-001.wav"}, write)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	if want := "ffconcat version 1.0\nfile 'seg-000.wav'\nfile 'gap.wav'\nfile 'seg-001.wav'\n"; string(body) != want {
		t.Fatalf("list =\n%s", body)
	}
	for _, bad := range []string{"../x.wav", "/etc/passwd", "a/b.wav", "x'.wav", "http:x", ".."} {
		if _, err := WriteConcatList(dir, "list.txt", []string{bad}, write); !IsRefused(err) {
			t.Fatalf("entry %q accepted", bad)
		}
	}
}

func TestParseProgressReportsPercentOfTotal(t *testing.T) {
	var got []int
	parseProgress(strings.NewReader("frame=1\nout_time_us=500000\nprogress=continue\nout_time_us=1000000\nprogress=end\n"), 2000, func(p int) { got = append(got, p) })
	if !slices.Equal(got, []int{25, 50, 100}) {
		t.Fatalf("progress = %v", got)
	}
}

func TestFormatForMIME(t *testing.T) {
	for mime, want := range map[string]Format{"image/png": FormatImage2, "audio/wav": FormatWAV, "audio/mpeg": FormatMP3, "audio/flac": FormatFLAC} {
		if got, ok := FormatForMIME(mime); !ok || got != want {
			t.Fatalf("%s -> %q", mime, got)
		}
	}
	if _, ok := FormatForMIME("text/plain"); ok {
		t.Fatal("text/plain must have no ffmpeg format")
	}
}
