-- Measured narration speed per voice (see the voice_rate_calibrations
-- migration). Not tenant-scoped.

-- name: UpsertVoiceRateCalibration :exec
INSERT INTO voice_rate_calibrations (voice_key, engine, language, words, seconds, wpm, samples, run_id, measured_at)
VALUES (@voice_key, @engine, @language, @words, @seconds, @wpm, @samples, @run_id, now())
ON CONFLICT (voice_key) DO UPDATE SET
    engine = EXCLUDED.engine,
    language = EXCLUDED.language,
    words = EXCLUDED.words,
    seconds = EXCLUDED.seconds,
    wpm = EXCLUDED.wpm,
    samples = EXCLUDED.samples,
    run_id = EXCLUDED.run_id,
    measured_at = now();

-- name: GetVoiceRateCalibration :one
SELECT * FROM voice_rate_calibrations WHERE voice_key = @voice_key;
