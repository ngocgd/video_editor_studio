-- +goose Up
-- Measured narration speed per voice, written by the tts benchmark from
-- real synthesized audio and read by the duration estimate instead of
-- its uncalibrated language defaults. Not tenant-scoped: like the model
-- benchmarks it describes an engine and a voice on this deployment's GPU,
-- keyed by engine plus built-in voice name or reference-audio digest, so
-- no tenant content is stored.
CREATE TABLE voice_rate_calibrations (
    voice_key text PRIMARY KEY,
    engine text NOT NULL,
    language text NOT NULL CHECK (language IN ('en', 'vi')),
    words bigint NOT NULL CHECK (words > 0),
    seconds double precision NOT NULL CHECK (seconds > 0),
    wpm double precision NOT NULL CHECK (wpm > 0),
    samples integer NOT NULL CHECK (samples > 0),
    run_id uuid NOT NULL,
    measured_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE voice_rate_calibrations;
