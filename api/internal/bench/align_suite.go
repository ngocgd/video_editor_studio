package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/providers/align"
)

// alignTargetSeconds is the narration length each align case builds (a
// 5-minute EN and a 5-minute VI audio).
const alignTargetSeconds = 300.0

// maxCueRunes is the Python aligner's cue length: a sentence longer than
// this is split into several cues, which would break the one sentence,
// one cue ground truth the drift is measured against.
const maxCueRunes = 84

var sentenceEnd = regexp.MustCompile(`([.!?\x{2026}])\s+`)

// Sentences splits a paragraph after . ! ? and the ellipsis, the same
// boundaries the Python aligner cuts subtitle cues at.
func Sentences(paragraph string) []string {
	marked := sentenceEnd.ReplaceAllString(strings.TrimSpace(paragraph), "$1\n")
	var out []string
	for _, s := range strings.Split(marked, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Cue is one aligned subtitle cue as the Python worker uploads it.
type Cue struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type cueFile struct {
	Granularity  string  `json:"granularity"`
	MatchedRatio float64 `json:"matched_ratio"`
	Segments     []Cue   `json:"segments"`
}

// AlignResult is one align case: the narration's length, how long the
// alignment took, and how far each cue lands from where its sentence
// really starts and ends in the audio.
type AlignResult struct {
	Name, Language string
	AudioSeconds   float64
	AlignSeconds   float64
	MinutesPerHour float64
	Cues           int
	MaxDriftS      float64
	MeanDriftS     float64
	MatchedRatio   float64
	Granularity    string
	SwitchSeconds  float64
	Switched       bool
	Err            error
}

// Drift compares cues to the true sentence spans (start offsets and
// lengths). It needs exactly one cue per sentence.
func Drift(cues []Cue, starts, lengths []float64) (maxDrift, meanDrift float64, err error) {
	if len(cues) != len(starts) {
		return 0, 0, fmt.Errorf("bench: %d cues for %d sentences", len(cues), len(starts))
	}
	var sum float64
	for i, c := range cues {
		d := max(math.Abs(c.Start-starts[i]), math.Abs(c.End-(starts[i]+lengths[i])))
		maxDrift = max(maxDrift, d)
		sum += d
	}
	return maxDrift, sum / float64(len(cues)), nil
}

// RunAlign builds about five minutes of narration per language from
// benchmark sentences synthesized one by one (so every sentence's true
// start and end in the joined audio is known), aligns the joined audio
// with the narration text and records the drift and the alignment speed.
func (r *VoiceRunner) RunAlign(ctx context.Context) ([]AlignResult, []Budget, error) {
	const suite = "align"
	runID := idconv.NewV7()
	if _, ok := r.Sink.Get(referenceName); !ok {
		ref, err := r.makeReference(ctx, runID, suite)
		if err != nil {
			return nil, nil, err
		}
		if ref.Err != nil {
			r.logf("no reference clip, the EN narration cannot be built: %v", ref.Err)
		}
	}
	var results []AlignResult
	for _, lang := range []string{"en", "vi"} {
		res := r.alignCase(ctx, lang)
		results = append(results, res)
		if err := r.recordAlign(ctx, runID, suite, res); err != nil {
			return results, nil, err
		}
		if ctx.Err() != nil {
			return results, nil, ctx.Err()
		}
	}
	return results, alignBudgets(results), nil
}

func (r *VoiceRunner) alignCase(ctx context.Context, lang string) AlignResult {
	res := AlignResult{Name: "align-" + lang, Language: lang}
	engine, voice, clone := EngineVI, VoiceVI, false
	if lang == "en" {
		engine, voice, clone = EngineEN, "", true
	}

	var clips []WAV
	var texts []string
	var total float64
	for _, p := range Paragraphs(lang) {
		for _, s := range Sentences(p) {
			if total >= alignTargetSeconds {
				break
			}
			if len([]rune(s)) > maxCueRunes {
				continue
			}
			v := r.synthesize(ctx, fmt.Sprintf("%s-s%03d", res.Name, len(clips)+1), engine, lang, voice, s, clone)
			if v.Err != nil {
				res.Err = fmt.Errorf("bench: building the %s narration: %w", lang, v.Err)
				return res
			}
			if len(clips) > 0 {
				total += clipGapS
			}
			clips = append(clips, v.Audio)
			texts = append(texts, s)
			total += v.Audio.Seconds()
		}
	}
	return r.alignAudio(ctx, res, clips, texts)
}

// alignAudio joins clips (one sentence each), aligns the result against
// the sentences' text and measures every cue's drift from its sentence.
func (r *VoiceRunner) alignAudio(ctx context.Context, res AlignResult, clips []WAV, texts []string) AlignResult {
	lang := res.Language
	joined, starts, err := ConcatWAV(clips, clipGapS)
	if err != nil {
		res.Err = err
		return res
	}
	lengths := make([]float64, len(clips))
	for i, c := range clips {
		lengths[i] = c.Seconds()
	}
	res.AudioSeconds = joined.Seconds()
	audioName, cueName := res.Name+".wav", res.Name+".json"
	r.Sink.Put(audioName, joined.Bytes())

	res.SwitchSeconds, res.Switched, res.Err = r.ensure(ctx, pyworkerBackend, EngineAlign)
	if res.Err != nil {
		return res
	}
	start := time.Now()
	out, err := r.Align.Align(ctx, align.Request{
		Engine: EngineAlign, AudioGetURL: r.Sink.URL(audioName), Text: strings.Join(texts, " "),
		OutputPutURL: r.Sink.URL(cueName), Params: map[string]string{"language": lang, "output_key": cueName},
	}, nil)
	res.AlignSeconds = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return res
	}
	res.MatchedRatio, _ = strconv.ParseFloat(out.Metadata["matched_ratio"], 64)
	raw, ok := r.Sink.Get(cueName)
	if !ok {
		res.Err = fmt.Errorf("bench: %s: the worker reported success but uploaded no cues", res.Name)
		return res
	}
	r.save(cueName, raw)
	var cues cueFile
	if err := json.Unmarshal(raw, &cues); err != nil {
		res.Err = fmt.Errorf("bench: %s: cue file: %w", res.Name, err)
		return res
	}
	res.Cues, res.Granularity = len(cues.Segments), cues.Granularity
	if res.AudioSeconds > 0 {
		res.MinutesPerHour = res.AlignSeconds / res.AudioSeconds * 60
	}
	res.MaxDriftS, res.MeanDriftS, res.Err = Drift(cues.Segments, starts, lengths)
	return res
}

func (r *VoiceRunner) recordAlign(ctx context.Context, runID uuid.UUID, suite string, res AlignResult) error {
	meta := map[string]any{
		"language": res.Language, "audio_s": res.AudioSeconds, "cues": res.Cues,
		"max_drift_s": res.MaxDriftS, "mean_drift_s": res.MeanDriftS, "minutes_per_hour": res.MinutesPerHour,
		"matched_ratio": res.MatchedRatio, "granularity": res.Granularity,
	}
	r.logResult(res.Name, EngineAlign, res.Err, "%6.1fs audio aligned in %6.1fs (%.2f min/h)  drift max %.2fs mean %.2fs",
		res.AudioSeconds, res.AlignSeconds, res.MinutesPerHour, res.MaxDriftS, res.MeanDriftS)
	return recordRow(ctx, r.Queries, runID, row{
		Suite: suite, Case: res.Name, Model: EngineAlign, Seconds: res.AlignSeconds,
		SwitchSeconds: res.SwitchSeconds, Switched: res.Switched, Err: res.Err, Meta: meta,
	})
}

func alignBudgets(results []AlignResult) []Budget {
	speed := Budget{Name: "alignment time (worst)", Limit: BudgetAlignMinutesPerHr, Unit: "min/h"}
	for _, res := range results {
		if res.Err == nil {
			speed.Measured, speed.Known = max(speed.Measured, res.MinutesPerHour), true
		}
	}
	return []Budget{speed}
}
