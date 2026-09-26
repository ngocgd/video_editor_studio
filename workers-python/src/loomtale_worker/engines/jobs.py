"""Request and result shapes the TTS and align engines exchange with the
gRPC servicers. Engines never see gRPC types or URLs: the servicers
download inputs to local files, hand engines a job, and upload whatever
the engine returns.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

import numpy as np

# Progress callbacks take a percentage (0-100). They are called from the
# engine's worker thread; the servicer makes them thread-safe.
ProgressFn = Callable[[int], None]


def _no_progress(_pct: int) -> None:
    return None


@dataclass
class SynthesisJob:
    """One narration request. reference_wav is the voice preset's
    reference clip for cloning (only set when the preset's consent flag
    was checked); voice names a built-in voice for engines that have
    them."""

    text: str
    language: str
    voice: str = ""
    reference_wav: Path | None = None
    params: dict[str, str] = field(default_factory=dict)
    progress: ProgressFn = _no_progress


@dataclass
class SynthesisOutput:
    """Mono float32 samples in [-1, 1] at sample_rate."""

    samples: np.ndarray
    sample_rate: int

    @property
    def duration_s(self) -> float:
        return float(len(self.samples)) / float(self.sample_rate)


@dataclass
class AlignJob:
    """Aligns the known narration text to an audio file."""

    audio_path: Path
    text: str
    language: str
    progress: ProgressFn = _no_progress


@dataclass
class TimedWord:
    word: str
    start: float
    end: float


@dataclass
class TimedSegment:
    """One subtitle cue. words is filled for word-level alignment (EN)."""

    text: str
    start: float
    end: float
    words: list[TimedWord] = field(default_factory=list)


@dataclass
class AlignOutput:
    language: str
    granularity: str  # "word" or "segment"
    segments: list[TimedSegment]
    audio_duration_s: float
    matched_ratio: float  # share of script words matched to recognised words

    def to_json_dict(self) -> dict:
        return {
            "language": self.language,
            "granularity": self.granularity,
            "audio_duration_s": round(self.audio_duration_s, 3),
            "matched_ratio": round(self.matched_ratio, 4),
            "segments": [
                {
                    "text": s.text,
                    "start": round(s.start, 3),
                    "end": round(s.end, 3),
                    **(
                        {
                            "words": [
                                {"word": w.word, "start": round(w.start, 3), "end": round(w.end, 3)}
                                for w in s.words
                            ]
                        }
                        if s.words
                        else {}
                    ),
                }
                for s in self.segments
            ],
        }
