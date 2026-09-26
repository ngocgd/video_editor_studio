"""Request and result shapes the engines exchange with the
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


@dataclass
class ScoreJob:
    """Scores how closely image_path shows the same character as the
    reference images (the character's approved refs)."""

    image_path: Path
    reference_paths: list[Path]
    params: dict[str, str] = field(default_factory=dict)


@dataclass
class ScoreOutput:
    """score is the mean cosine similarity to the references, in [-1, 1];
    metadata carries the spread (min/max) and the reference count."""

    score: float
    metadata: dict[str, str] = field(default_factory=dict)


@dataclass
class DepthJob:
    """Estimates a relative depth map for one image."""

    image_path: Path
    params: dict[str, str] = field(default_factory=dict)


@dataclass
class DepthOutput:
    """A 16-bit grayscale PNG the size of the input image (brighter is
    nearer: Depth Anything predicts relative inverse depth)."""

    png: bytes
    width: int
    height: int


# Train callbacks: (step, total_steps) and one scrubbed log line. Like
# ProgressFn they are called from the engine's worker thread.
StepFn = Callable[[int, int], None]
LogFn = Callable[[str], None]


def _no_step(_step: int, _total: int) -> None:
    return None


def _no_log(_line: str) -> None:
    return None


@dataclass
class TrainJob:
    """Trains one character LoRA on the images (and optional same-named
    .txt captions) in dataset_dir, writing the weights under work_dir.
    cancelled is a threading.Event-like flag the engine polls so an
    abandoned RPC stops the trainer instead of holding the GPU."""

    dataset_dir: Path
    work_dir: Path
    base_model: str
    params: dict[str, str] = field(default_factory=dict)
    step: StepFn = _no_step
    log: LogFn = _no_log
    cancelled: Callable[[], bool] = lambda: False


@dataclass
class TrainOutput:
    """weights is the trained LoRA (a single .safetensors file)."""

    weights: Path
    metadata: dict[str, str] = field(default_factory=dict)
