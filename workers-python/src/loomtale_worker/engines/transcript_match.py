"""Maps the known narration script onto speech-recognition output. The
recogniser gives timed words that may differ from the script (numbers
spelled out, a misheard name); matching normalised word sequences and
interpolating across the gaps gives every script word a time, so the
subtitles always show the script's own text.
"""

from __future__ import annotations

import difflib
import re
import unicodedata
from dataclasses import dataclass

from loomtale_worker.engines.jobs import TimedSegment, TimedWord
from loomtale_worker.engines.text_chunks import subtitle_cues

_NON_WORD = re.compile(r"[^\w]+", re.UNICODE)


def normalize_word(word: str) -> str:
    """Lower-cases, NFC-normalises (so Vietnamese diacritics compare
    equal whichever way they were composed) and strips punctuation."""
    return _NON_WORD.sub("", unicodedata.normalize("NFC", word).lower())


@dataclass(frozen=True)
class RecognisedWord:
    word: str
    start: float
    end: float


@dataclass(frozen=True)
class Match:
    times: list[tuple[float, float]]  # one (start, end) per script word
    matched_ratio: float


def match_script(
    script_words: list[str], recognised: list[RecognisedWord], duration_s: float
) -> Match:
    """Gives every script word a (start, end). Words matched to a
    recognised word take its times; runs of unmatched words share the
    gap between their matched neighbours in proportion to their length.
    """
    n = len(script_words)
    if n == 0:
        return Match(times=[], matched_ratio=1.0)
    times: list[tuple[float, float] | None] = [None] * n
    a = [normalize_word(w) for w in script_words]
    b = [normalize_word(r.word) for r in recognised]
    matcher = difflib.SequenceMatcher(None, a, b, autojunk=False)
    matched = 0
    for block in matcher.get_matching_blocks():
        for k in range(block.size):
            r = recognised[block.b + k]
            times[block.a + k] = (r.start, r.end)
            matched += 1
    filled = _interpolate(script_words, times, duration_s)
    return Match(times=filled, matched_ratio=matched / n)


def _interpolate(
    words: list[str], times: list[tuple[float, float] | None], duration_s: float
) -> list[tuple[float, float]]:
    out = list(times)
    i = 0
    while i < len(out):
        if out[i] is not None:
            i += 1
            continue
        j = i
        while j < len(out) and out[j] is None:
            j += 1
        left = out[i - 1][1] if i > 0 else 0.0
        right = out[j][0] if j < len(out) else duration_s
        right = max(right, left)
        weights = [max(len(normalize_word(w)), 1) for w in words[i:j]]
        total = float(sum(weights))
        t = left
        for k, weight in zip(range(i, j), weights, strict=True):
            span = (right - left) * weight / total
            out[k] = (t, t + span)
            t += span
        i = j
    return [(float(s), float(e)) for s, e in out]  # type: ignore[misc]


def build_cues(
    text: str, word_times: list[tuple[float, float]], with_words: bool
) -> list[TimedSegment]:
    """Groups the script's words (in order) into subtitle cues and times
    each cue from its first and last word. word_times must hold one entry
    per whitespace-delimited word of text."""
    words = text.split()
    if len(words) != len(word_times):
        raise ValueError(f"{len(words)} script words but {len(word_times)} word times")
    cues: list[TimedSegment] = []
    i = 0
    for cue in subtitle_cues(text):
        count = len(cue.split())
        span = word_times[i : i + count]
        timed = [
            TimedWord(word=w, start=s, end=e)
            for w, (s, e) in zip(words[i : i + count], span, strict=True)
        ]
        cues.append(
            TimedSegment(
                text=cue, start=span[0][0], end=span[-1][1], words=timed if with_words else []
            )
        )
        i += count
    return cues
