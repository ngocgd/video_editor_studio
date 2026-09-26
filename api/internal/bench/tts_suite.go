package bench

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/speechrate"
)

// texts holds the benchmark narration: 30 EN and 30 VI paragraphs of
// the channel's genre, separated by blank lines.
//
//go:embed texts/*.txt
var texts embed.FS

// calibrationFitCount is how many paragraphs per language fit the
// speech-rate calibration; the rest check its predictions.
const calibrationFitCount = 20

// Paragraphs returns the benchmark paragraphs for lang ("en" or "vi").
func Paragraphs(lang string) []string {
	raw, err := texts.ReadFile("texts/" + lang + ".txt")
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n") {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func countWords(text string) int { return len(strings.Fields(text)) }

// VoiceCalibration is one voice's fitted rate and its check on held-out
// paragraphs: the duration those paragraphs were predicted to take at the
// fitted rate against their real audio length.
type VoiceCalibration struct {
	speechrate.Calibration
	HoldoutPredictedS float64
	HoldoutActualS    float64
	HoldoutDeviation  float64
	HoldoutChecked    bool
}

// TTSReport is a tts suite run.
type TTSReport struct {
	RunID        uuid.UUID
	Results      []VoiceResult
	Calibrations []VoiceCalibration
	Budgets      []Budget
}

// RunTTS synthesizes every EN paragraph with Chatterbox (cloning a
// synthetic reference voice) and every VI paragraph with VieNeu's
// built-in voice, recording real-time factor, VRAM peak and switch time
// per case, then fits and stores each voice's words per minute.
func (r *VoiceRunner) RunTTS(ctx context.Context) (TTSReport, error) {
	const suite = "tts"
	report := TTSReport{RunID: idconv.NewV7()}
	ref, err := r.makeReference(ctx, report.RunID, suite)
	if err != nil {
		return report, err
	}
	report.Results = append(report.Results, ref)

	type voice struct{ engine, lang, voice string }
	for _, v := range []voice{{EngineEN, "en", ""}, {EngineVI, "vi", VoiceVI}} {
		clone := v.engine == EngineEN
		var samples []VoiceResult
		for i, p := range Paragraphs(v.lang) {
			if clone && ref.Err != nil {
				res := VoiceResult{Name: fmt.Sprintf("%s-%02d", v.lang, i+1), Engine: v.engine, Language: v.lang, Err: fmt.Errorf("bench: no reference clip to clone: %w", ref.Err)}
				report.Results = append(report.Results, res)
				if err := r.recordVoice(ctx, report.RunID, suite, res); err != nil {
					return report, err
				}
				continue
			}
			res := r.synthesize(ctx, fmt.Sprintf("%s-%02d", v.lang, i+1), v.engine, v.lang, v.voice, p, clone)
			report.Results = append(report.Results, res)
			if err := r.recordVoice(ctx, report.RunID, suite, res); err != nil {
				return report, err
			}
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			if res.Err == nil {
				samples = append(samples, res)
			}
		}
		// A cloned voice is keyed by the reference file it clones, the
		// same bytes a voice preset stores as its reference asset.
		var refAudio []byte
		if clone {
			refAudio, _ = r.Sink.Get(referenceName)
		}
		cal, ok := calibrate(speechrate.VoiceKey(v.engine, v.voice, refAudio), v.engine, v.lang, samples)
		if !ok {
			continue
		}
		report.Calibrations = append(report.Calibrations, cal)
		if r.Rates != nil {
			if err := r.Rates.Save(ctx, cal.Calibration, report.RunID); err != nil {
				return report, err
			}
		}
	}
	report.Budgets = ttsBudgets(report)
	return report, nil
}

// calibrate fits on the first calibrationFitCount samples and checks the
// rest, then refits on all of them for the stored rate.
func calibrate(key, engine, lang string, results []VoiceResult) (VoiceCalibration, bool) {
	samples := make([]speechrate.Sample, 0, len(results))
	for _, res := range results {
		samples = append(samples, speechrate.Sample{Words: res.Words, Seconds: res.AudioSeconds})
	}
	all, err := speechrate.Calibrate(key, engine, lang, samples)
	if err != nil {
		return VoiceCalibration{}, false
	}
	out := VoiceCalibration{Calibration: all}
	if len(samples) > calibrationFitCount {
		fit, err := speechrate.Calibrate(key, engine, lang, samples[:calibrationFitCount])
		if err == nil {
			for _, s := range samples[calibrationFitCount:] {
				out.HoldoutPredictedS += speechrate.PredictSeconds(s.Words, fit.WPM)
				out.HoldoutActualS += s.Seconds
			}
			out.HoldoutDeviation = speechrate.Deviation(out.HoldoutPredictedS, out.HoldoutActualS)
			out.HoldoutChecked = true
		}
	}
	return out, true
}

func ttsBudgets(r TTSReport) []Budget {
	var budgets []Budget
	for _, engine := range []string{EngineEN, EngineVI} {
		worst := Budget{Name: engine + " real-time factor (worst)", Limit: BudgetTTSRealTimeFactor, Unit: "x"}
		for _, res := range r.Results {
			if res.Engine == engine && res.Err == nil && res.AudioSeconds > 0 {
				worst.Measured, worst.Known = max(worst.Measured, res.RTF), true
			}
		}
		budgets = append(budgets, worst)
	}
	switches := Budget{Name: "residency switch (worst)", Limit: BudgetResidencySwitchS, Unit: "s"}
	for _, res := range r.Results {
		if res.Switched {
			switches.Measured, switches.Known = max(switches.Measured, res.SwitchSeconds), true
		}
	}
	budgets = append(budgets, switches)
	for _, c := range r.Calibrations {
		budgets = append(budgets, Budget{
			Name: c.Engine + " predicted vs actual duration", Measured: c.HoldoutDeviation,
			Limit: BudgetDurationDeviation, Unit: "ratio", Known: c.HoldoutChecked,
		})
	}
	return budgets
}
