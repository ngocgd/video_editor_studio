// Package duration estimates spoken runtime from a word count. Rates are
// fixed defaults (EN 150 wpm, VI 165 wpm) until phase 9b measures real
// narration rates per voice preset; every result is marked Uncalibrated so
// callers can show "estimated" in the UI.
package duration

// defaultWPM is the fallback words-per-minute rate per language, used when
// no per-voice-preset measured rate is available (always true before phase
// 9b).
var defaultWPM = map[string]float64{
	"en": 150,
	"vi": 165,
}

// fallbackWPM is used for a language with no configured default (e.g. an
// unexpected value slipping through validation).
const fallbackWPM = 150

// EstimateResult is the outcome of Estimate.
type EstimateResult struct {
	Minutes float64
	// Uncalibrated is always true until phase 9b writes measured
	// per-voice-preset rates; the UI renders an "uncalibrated" badge
	// whenever this is set.
	Uncalibrated bool
	// WPM is the rate actually used, for display/debugging.
	WPM float64
}

// Estimate returns the estimated spoken duration for words at the given
// language's default rate. voicePreset is accepted for a future per-voice
// calibrated rate (phase 9b) but does not yet affect the result.
func Estimate(words int, lang string, voicePreset string) EstimateResult {
	_ = voicePreset // reserved for phase 9b's per-voice calibrated rates
	wpm, ok := defaultWPM[lang]
	if !ok || wpm <= 0 {
		wpm = fallbackWPM
	}
	if words < 0 {
		words = 0
	}
	return EstimateResult{
		Minutes:      float64(words) / wpm,
		Uncalibrated: true,
		WPM:          wpm,
	}
}
