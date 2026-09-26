package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/pipeline"
	"loomtale/api/internal/providers/align"
	"loomtale/api/internal/providers/tts"
	"loomtale/api/internal/speechrate"
)

// Engines and voices the voice suites use (manifest entry names).
const (
	EngineEN    = "chatterbox"
	EngineVI    = "vieneu-v3-turbo"
	EngineAlign = "whisper-align"
	// VoiceVI is a VieNeu built-in voice; it also speaks the reference
	// clip Chatterbox clones, a synthetic voice with no person behind it,
	// so the consent flag is trivially satisfied.
	VoiceVI = "Mai Anh"
	// referenceText is spoken by VoiceVI to make that reference clip.
	referenceText = "Xin chào các bạn. Hôm nay trời trong xanh, gió nhẹ thổi qua rặng tre đầu làng."
	referenceName = "reference.wav"
)

// Performance budgets checked by the voice and llm suites.
const (
	BudgetTTSRealTimeFactor = 0.3
	BudgetAlignMinutesPerHr = 10.0
	BudgetLLMFirstTokenS    = 3.0
	BudgetResidencySwitchS  = 15.0
	BudgetDurationDeviation = 0.10
	pyworkerBackend         = "pyworker"
	ollamaBackend           = "ollama"
	clipGapS                = 0.3
)

// VoiceRunner drives the Python worker's TTS and Align engines through
// the residency manager, storing audio in a Sink and one
// model_benchmarks row per case.
type VoiceRunner struct {
	TTS       *tts.Client
	Align     *align.Client
	Residency pipeline.ModelResidency
	Queries   dbgen.Querier
	Sink      *Sink
	// Rates, if set, receives the measured words per minute per voice.
	Rates  *speechrate.Store
	OutDir string
	Log    func(format string, args ...any)
}

// VoiceResult is one synthesized passage.
type VoiceResult struct {
	Name, Engine, Language, Voice string
	Words                         int
	AudioSeconds, WallSeconds     float64
	RTF                           float64
	VRAMPeakMB                    int64
	SwitchSeconds                 float64
	Switched                      bool
	Err                           error
	Audio                         WAV
}

// ensure makes backend:model resident, timing the switch when one is
// needed.
func (r *VoiceRunner) ensure(ctx context.Context, backend, model string) (float64, bool, error) {
	target := pipeline.ModelRef{Backend: backend, Model: model}
	if current := r.Residency.Current(); current != nil && *current == target {
		return 0, false, nil
	}
	start := time.Now()
	if err := r.Residency.Ensure(ctx, target); err != nil {
		return 0, false, fmt.Errorf("bench: load %s:%s: %w", backend, model, err)
	}
	return time.Since(start).Seconds(), true, nil
}

// synthesize runs one TTS case. A clone uses the reference clip already
// in the sink.
func (r *VoiceRunner) synthesize(ctx context.Context, name, engine, lang, voice, text string, clone bool) VoiceResult {
	res := VoiceResult{Name: name, Engine: engine, Language: lang, Voice: voice, Words: countWords(text)}
	res.SwitchSeconds, res.Switched, res.Err = r.ensure(ctx, pyworkerBackend, engine)
	if res.Err != nil {
		return res
	}
	params := map[string]string{"language": lang, "output_key": name + ".wav"}
	if clone {
		params["reference_url"] = r.Sink.URL(referenceName)
		params["consent"] = "granted"
	}
	start := time.Now()
	out, err := r.TTS.Synthesize(ctx, tts.Request{
		Engine: engine, Voice: voice, Text: text, OutputPutURL: r.Sink.URL(name + ".wav"), Params: params,
	}, nil)
	res.WallSeconds = time.Since(start).Seconds()
	if err != nil {
		res.Err = err
		return res
	}
	res.AudioSeconds = out.DurationS
	res.RTF, _ = strconv.ParseFloat(out.Metadata["rtf"], 64)
	res.VRAMPeakMB, _ = strconv.ParseInt(out.Metadata["vram_peak_mb"], 10, 64)
	data, ok := r.Sink.Get(name + ".wav")
	if !ok {
		res.Err = fmt.Errorf("bench: %s: the worker reported success but uploaded no audio", name)
		return res
	}
	if res.Audio, res.Err = ParseWAV(data); res.Err == nil {
		r.save(name+".wav", data)
	}
	return res
}

// makeReference synthesizes the clip Chatterbox clones and stores it in
// the sink under referenceName.
func (r *VoiceRunner) makeReference(ctx context.Context, runID uuid.UUID, suite string) (VoiceResult, error) {
	res := r.synthesize(ctx, "reference", EngineVI, "vi", VoiceVI, referenceText, false)
	if err := r.recordVoice(ctx, runID, suite, res); err != nil {
		return res, err
	}
	if res.Err != nil {
		return res, nil
	}
	r.Sink.Put(referenceName, res.Audio.Bytes())
	return res, nil
}

func (r *VoiceRunner) recordVoice(ctx context.Context, runID uuid.UUID, suite string, res VoiceResult) error {
	meta := map[string]any{
		"language": res.Language, "voice": res.Voice, "words": res.Words,
		"audio_s": res.AudioSeconds, "rtf": res.RTF, "switched": res.Switched,
	}
	if res.AudioSeconds > 0 {
		meta["wpm"] = float64(res.Words) / (res.AudioSeconds / 60)
	}
	r.logResult(res.Name, res.Engine, res.Err, "%6.1fs audio in %6.1fs  rtf %.3f  vram %5d MB  switch %5.1fs",
		res.AudioSeconds, res.WallSeconds, res.RTF, res.VRAMPeakMB, res.SwitchSeconds)
	return recordRow(ctx, r.Queries, runID, row{
		Suite: suite, Case: res.Name, Model: res.Engine, Seconds: res.WallSeconds, VRAMPeakMB: res.VRAMPeakMB,
		SwitchSeconds: res.SwitchSeconds, Switched: res.Switched, Err: res.Err, Meta: meta,
	})
}

// row is one model_benchmarks row of the voice and llm suites.
type row struct {
	Suite, Case, Model string
	Seconds            float64
	VRAMPeakMB         int64
	SwitchSeconds      float64
	Switched           bool
	Err                error
	Meta               map[string]any
}

func recordRow(ctx context.Context, q dbgen.Querier, runID uuid.UUID, r row) error {
	raw, _ := json.Marshal(r.Meta)
	ok := r.Err == nil
	params := dbgen.InsertModelBenchmarkParams{
		ID: idconv.ToPg(idconv.NewV7()), RunID: idconv.ToPg(runID), Suite: r.Suite,
		CaseName: r.Case, Model: r.Model, Ok: ok, Meta: raw,
	}
	if ok {
		params.Seconds = float8(r.Seconds)
		if r.VRAMPeakMB > 0 {
			params.VramPeakMb = idconv.ToPgInt8(r.VRAMPeakMB)
		}
	}
	if r.Switched {
		params.SwitchSeconds = float8(r.SwitchSeconds)
	}
	if r.Err != nil {
		params.Error = idconv.ToPgText(r.Err.Error())
	}
	return q.InsertModelBenchmark(ctx, params)
}

func (r *VoiceRunner) save(name string, data []byte) {
	if r.OutDir == "" {
		return
	}
	path := filepath.Join(r.OutDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		r.logf("could not save %s: %v", path, err)
	}
}

func (r *VoiceRunner) logResult(name, model string, err error, format string, args ...any) {
	if err != nil {
		r.logf("%-20s %-18s FAILED: %v", name, model, err)
		return
	}
	r.logf("%-20s %-18s "+format, append([]any{name, model}, args...)...)
}

func (r *VoiceRunner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// Budget is one performance budget check.
type Budget struct {
	Name     string
	Measured float64
	Limit    float64
	Unit     string
	// Known is false when nothing was measured (e.g. every case failed).
	Known bool
}

// OK reports whether the measured value is within the limit.
func (b Budget) OK() bool { return b.Known && b.Measured <= b.Limit }

func (b Budget) String() string {
	if !b.Known {
		return fmt.Sprintf("%-34s not measured (limit %.2f %s)", b.Name, b.Limit, b.Unit)
	}
	verdict := "within budget"
	if !b.OK() {
		verdict = "OVER BUDGET"
	}
	return fmt.Sprintf("%-34s %8.3f %s (limit %.2f): %s", b.Name, b.Measured, b.Unit, b.Limit, verdict)
}
