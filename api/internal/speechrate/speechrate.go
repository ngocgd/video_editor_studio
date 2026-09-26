// Package speechrate measures how fast each narration voice actually
// speaks and stores it, so the duration estimate (words / words per
// minute) can use a measured rate per voice instead of its uncalibrated
// language defaults. The tts benchmark writes the calibrations from real
// synthesized audio; the estimate reads them by voice key.
package speechrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// VoiceKey identifies a voice across runs: the engine plus either its
// built-in voice name or the digest of the reference audio it clones
// (so re-uploading the same clip keeps its calibration, and a different
// clip under the same preset name gets its own).
func VoiceKey(engine, builtinVoice string, reference []byte) string {
	if len(reference) > 0 {
		sum := sha256.Sum256(reference)
		return engine + ":ref:" + hex.EncodeToString(sum[:])[:16]
	}
	return engine + ":voice:" + builtinVoice
}

// Sample is one synthesized passage: its word count (whitespace-split,
// the rule the duration estimate uses) and the audio's length.
type Sample struct {
	Words   int
	Seconds float64
}

// Calibration is a voice's measured rate over a set of samples.
type Calibration struct {
	VoiceKey string
	Engine   string
	Language string
	Words    int64
	Seconds  float64
	WPM      float64
	Samples  int
}

// Calibrate pools samples (total words over total minutes, so long
// passages weigh more than short ones, as they do in an episode).
func Calibrate(voiceKey, engine, language string, samples []Sample) (Calibration, error) {
	c := Calibration{VoiceKey: voiceKey, Engine: engine, Language: language}
	for _, s := range samples {
		if s.Words <= 0 || s.Seconds <= 0 {
			continue
		}
		c.Words += int64(s.Words)
		c.Seconds += s.Seconds
		c.Samples++
	}
	if c.Samples == 0 {
		return Calibration{}, errors.New("speechrate: no usable samples")
	}
	c.WPM = float64(c.Words) / (c.Seconds / 60)
	return c, nil
}

// PredictSeconds is the duration estimate at wpm for words.
func PredictSeconds(words int, wpm float64) float64 {
	if wpm <= 0 {
		return 0
	}
	return float64(words) / wpm * 60
}

// Deviation is |predicted - actual| / actual, the calibration's accuracy
// on passages it was not fitted to.
func Deviation(predicted, actual float64) float64 {
	if actual <= 0 {
		return math.Inf(1)
	}
	return math.Abs(predicted-actual) / actual
}

// Store persists calibrations.
type Store struct {
	Queries dbgen.Querier
}

// Save upserts c, recording the benchmark run that measured it.
func (s *Store) Save(ctx context.Context, c Calibration, runID uuid.UUID) error {
	return s.Queries.UpsertVoiceRateCalibration(ctx, dbgen.UpsertVoiceRateCalibrationParams{
		VoiceKey: c.VoiceKey, Engine: c.Engine, Language: c.Language,
		Words: c.Words, Seconds: c.Seconds, Wpm: c.WPM, Samples: int32(min(c.Samples, math.MaxInt32)), //nolint:gosec // bounded by min
		RunID: idconv.ToPg(runID),
	})
}

// Lookup returns the measured words per minute for voiceKey, or ok false
// when the voice has not been calibrated yet.
func (s *Store) Lookup(ctx context.Context, voiceKey string) (wpm float64, ok bool, err error) {
	row, err := s.Queries.GetVoiceRateCalibration(ctx, voiceKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("speechrate: lookup %s: %w", voiceKey, err)
	}
	return row.Wpm, true, nil
}
