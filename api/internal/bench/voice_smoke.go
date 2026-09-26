package bench

import (
	"context"

	"loomtale/api/internal/db/idconv"
)

// voiceSmokeEN is the smoke suite's English line; it is aligned too.
const voiceSmokeEN = "The old master raised his sword, and the mountain answered with thunder."

// SmokeReport is a voice-smoke run.
type SmokeReport struct {
	Voices  []VoiceResult
	Align   AlignResult
	Budgets []Budget
}

// RunVoiceSmoke is the quick end-to-end check: one VI line (VieNeu,
// built-in voice, which is also the reference clip), one EN line
// (Chatterbox cloning it), a residency switch to the Ollama model and
// back to TTS when ollamaModel is set, and an alignment of the EN line.
func (r *VoiceRunner) RunVoiceSmoke(ctx context.Context, ollamaModel string) (SmokeReport, error) {
	const suite = "voice-smoke"
	runID := idconv.NewV7()
	var report SmokeReport

	vi, err := r.makeReference(ctx, runID, suite)
	if err != nil {
		return report, err
	}
	report.Voices = append(report.Voices, vi)

	en := r.synthesize(ctx, "en-line", EngineEN, "en", "", voiceSmokeEN, true)
	report.Voices = append(report.Voices, en)
	if err := r.recordVoice(ctx, runID, suite, en); err != nil {
		return report, err
	}

	if ollamaModel != "" {
		toLLM := VoiceResult{Name: "switch-to-ollama", Engine: ollamaModel}
		toLLM.SwitchSeconds, toLLM.Switched, toLLM.Err = r.ensure(ctx, ollamaBackend, ollamaModel)
		report.Voices = append(report.Voices, toLLM)
		if err := r.recordVoice(ctx, runID, suite, toLLM); err != nil {
			return report, err
		}
		back := r.synthesize(ctx, "vi-after-ollama", EngineVI, "vi", VoiceVI, referenceText, false)
		report.Voices = append(report.Voices, back)
		if err := r.recordVoice(ctx, runID, suite, back); err != nil {
			return report, err
		}
	}

	report.Align = AlignResult{Name: "align-en-line", Language: "en", Err: en.Err}
	if en.Err == nil {
		report.Align = r.alignAudio(ctx, report.Align, []WAV{en.Audio}, []string{voiceSmokeEN})
	}
	if err := r.recordAlign(ctx, runID, suite, report.Align); err != nil {
		return report, err
	}

	// ttsBudgets covers the real-time factors and every timed switch,
	// including the one to Ollama and back.
	report.Budgets = append(ttsBudgets(TTSReport{Results: report.Voices}), alignBudgets([]AlignResult{report.Align})...)
	return report, nil
}
