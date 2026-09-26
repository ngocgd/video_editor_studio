package render

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"loomtale/api/internal/media/ffmpeg"
)

// AudioSampleRate is the narration master's sample rate. It divides by
// every supported frame rate, so a frame is a whole number of samples.
const AudioSampleRate = 48000

// loudnessRange is the loudnorm LRA target in LU.
const loudnessRange = 11

// AudioLabel is the graph label the audio master comes out on.
const AudioLabel = "a"

// SamplesForFrames is the exact sample count of frames at fps.
func SamplesForFrames(frames, fps int) int { return frames * (AudioSampleRate / fps) }

// LoudnormStats is loudnorm's first-pass measurement.
type LoudnormStats struct {
	InputI       string `json:"input_i"`
	InputTP      string `json:"input_tp"`
	InputLRA     string `json:"input_lra"`
	InputThresh  string `json:"input_thresh"`
	TargetOffset string `json:"target_offset"`
}

// ParseLoudnorm reads the JSON block loudnorm prints (print_format=json)
// at the end of a first-pass log.
func ParseLoudnorm(log string) (LoudnormStats, error) {
	end := strings.LastIndex(log, "}")
	start := strings.LastIndex(log[:max(end, 0)], "{")
	if start < 0 || end < start {
		return LoudnormStats{}, fmt.Errorf("render: loudnorm printed no measurement")
	}
	var st LoudnormStats
	if err := json.Unmarshal([]byte(log[start:end+1]), &st); err != nil {
		return LoudnormStats{}, fmt.Errorf("render: unreadable loudnorm measurement: %w", err)
	}
	// Silence measures as -inf, which is not a number: a master of
	// silence is a bug upstream, not something to normalise.
	if _, err := st.floats(); err != nil {
		return LoudnormStats{}, err
	}
	return st, nil
}

// AudioGraph lays the scene voices end to end on the timeline, each
// padded or trimmed to exactly its scene's samples, then normalises the
// result. Input i is scene i's voice. With stats nil it is the measuring
// first pass; with stats it is the linear second pass, resampled back to
// 48 kHz (loudnorm works at 192 kHz).
func AudioGraph(s Settings, t Timeline, stats *LoudnormStats) (*ffmpeg.Graph, error) {
	g := &ffmpeg.Graph{}
	concatIn := make([]string, len(t.Scenes))
	for i, span := range t.Scenes {
		n := SamplesForFrames(span.Frames, t.FPS)
		label := "s" + strconv.Itoa(i)
		concatIn[i] = label
		g.Chains = append(g.Chains, ffmpeg.Chain{
			In: []string{strconv.Itoa(i) + ":a"},
			Filters: []ffmpeg.Filter{
				ffmpeg.F("aformat", ffmpeg.O("sample_fmts", ffmpeg.Enum("fltp")),
					ffmpeg.O("sample_rates", ffmpeg.Int(AudioSampleRate)), ffmpeg.O("channel_layouts", ffmpeg.Enum("mono"))),
				ffmpeg.F("apad", ffmpeg.O("whole_len", ffmpeg.Int(n))),
				ffmpeg.F("atrim", ffmpeg.O("end_sample", ffmpeg.Int(n))),
				ffmpeg.F("asetpts", ffmpeg.O("expr", ffmpeg.Expr("PTS-STARTPTS"))),
			},
			Out: []string{label},
		})
	}
	norm := []ffmpeg.Opt{
		ffmpeg.O("i", ffmpeg.Float(float64(s.LoudnessLUFSx10)/10)),
		ffmpeg.O("tp", ffmpeg.Float(float64(s.TruePeakDBTPx10)/10)),
		ffmpeg.O("lra", ffmpeg.Int(loudnessRange)),
	}
	if stats == nil {
		norm = append(norm, ffmpeg.O("print_format", ffmpeg.Enum("json")))
	} else {
		measured, err := stats.floats()
		if err != nil {
			return nil, err
		}
		norm = append(norm,
			ffmpeg.O("measured_i", ffmpeg.Float(measured[0])), ffmpeg.O("measured_tp", ffmpeg.Float(measured[1])),
			ffmpeg.O("measured_lra", ffmpeg.Float(measured[2])), ffmpeg.O("measured_thresh", ffmpeg.Float(measured[3])),
			ffmpeg.O("offset", ffmpeg.Float(measured[4])), ffmpeg.O("linear", ffmpeg.Enum("true")))
	}
	final := []ffmpeg.Filter{
		ffmpeg.F("concat", ffmpeg.O("n", ffmpeg.Int(len(t.Scenes))), ffmpeg.O("v", ffmpeg.Int(0)), ffmpeg.O("a", ffmpeg.Int(1))),
		ffmpeg.F("loudnorm", norm...),
	}
	if stats != nil {
		final = append(final, ffmpeg.F("aresample", ffmpeg.O("osr", ffmpeg.Int(AudioSampleRate))))
	}
	g.Chains = append(g.Chains, ffmpeg.Chain{In: concatIn, Filters: final, Out: []string{AudioLabel}})
	return g, nil
}

func (st LoudnormStats) floats() ([5]float64, error) {
	var out [5]float64
	for i, v := range []string{st.InputI, st.InputTP, st.InputLRA, st.InputThresh, st.TargetOffset} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return out, fmt.Errorf("render: loudnorm measured %q; is the narration silent?", v)
		}
		out[i] = f
	}
	return out, nil
}

// Loudness is an ebur128 summary: integrated loudness and true peak.
type Loudness struct {
	IntegratedLUFS float64 `json:"integratedLufs"`
	TruePeakDBTP   float64 `json:"truePeakDbtp"`
}

// ParseEBUR128 reads the summary ebur128 (peak=true) prints at the end
// of a log.
func ParseEBUR128(log string) (Loudness, error) {
	at := strings.LastIndex(log, "Summary:")
	if at < 0 {
		return Loudness{}, fmt.Errorf("render: ebur128 printed no summary")
	}
	var l Loudness
	var gotI, gotPeak bool
	for _, line := range strings.Split(log[at:], "\n") {
		key, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		fields := strings.Fields(rest)
		if !ok || len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		switch key {
		case "I":
			l.IntegratedLUFS, gotI = v, true
		case "Peak":
			l.TruePeakDBTP, gotPeak = v, true
		}
	}
	if !gotI || !gotPeak {
		return Loudness{}, fmt.Errorf("render: ebur128 summary lacks loudness or true peak")
	}
	return l, nil
}
