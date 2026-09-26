"""Unit tests for CTC forced alignment, script-to-recognition matching and
cue building, run on synthetic data (no model needed)."""

from __future__ import annotations

import numpy as np
import pytest

from loomtale_worker.engines.ctc_align import align_words, forced_align
from loomtale_worker.engines.transcript_match import (
    RecognisedWord,
    build_cues,
    match_script,
    normalize_word,
)
from loomtale_worker.engines.whisper_align import SAMPLE_RATE, refine_cue_times

VOCAB = {"<pad>": 0, "|": 1, "A": 2, "B": 3, "C": 4, "'": 5}


def emissions_for(frames: list[int], vocab_size: int = 6) -> np.ndarray:
    """Log-probs where frame t strongly prefers token frames[t]."""
    lp = np.full((len(frames), vocab_size), np.log(0.01 / (vocab_size - 1)))
    for t, tok in enumerate(frames):
        lp[t, tok] = np.log(0.99)
    return lp


def test_forced_align_finds_each_token_span():
    # blank A A blank B B B blank blank C blank
    lp = emissions_for([0, 2, 2, 0, 3, 3, 3, 0, 0, 4, 0])
    assert forced_align(lp, [2, 3, 4]) == [(1, 3), (4, 7), (9, 10)]


def test_forced_align_needs_a_blank_between_repeated_tokens():
    lp = emissions_for([2, 0, 2, 0])
    assert forced_align(lp, [2, 2]) == [(0, 1), (2, 3)]
    assert forced_align(emissions_for([2]), [2, 2]) is None


def test_forced_align_empty_transcript():
    assert forced_align(emissions_for([0, 0]), []) == []


def test_align_words_skips_words_without_alignable_characters():
    # "ab 42 c": the digits have no characters in the vocabulary.
    lp = emissions_for([0, 2, 3, 0, 1, 0, 4, 4, 0])
    spans = align_words(lp, ["ab", "42", "c"], VOCAB)
    assert spans[0] == (1, 3)
    assert spans[1] is None
    assert spans[2] == (6, 8)


def test_normalize_word_folds_case_punctuation_and_unicode_composition():
    decomposed = "Trời,"  # o-horn + combining grave, as some inputs spell it
    assert normalize_word(decomposed) == normalize_word("trời")
    assert normalize_word("“Hello!”") == "hello"


def test_match_script_uses_recognised_times_and_interpolates_gaps():
    script = ["In", "1990,", "the", "sect", "fell."]
    recognised = [
        RecognisedWord("in", 0.0, 0.2),
        RecognisedWord("nineteen", 0.2, 0.6),
        RecognisedWord("ninety", 0.6, 1.0),
        RecognisedWord("the", 1.0, 1.2),
        RecognisedWord("sect", 1.2, 1.6),
        RecognisedWord("fell", 1.6, 2.0),
    ]
    m = match_script(script, recognised, duration_s=2.0)
    assert m.matched_ratio == pytest.approx(4 / 5)
    assert m.times[0] == (0.0, 0.2)
    assert m.times[1] == pytest.approx((0.2, 1.0))  # the unmatched year fills the gap
    assert m.times[4] == (1.6, 2.0)


def test_match_script_with_no_recognition_spreads_words_over_the_audio():
    m = match_script(["aa", "bb"], [], duration_s=4.0)
    assert m.matched_ratio == 0
    assert m.times == [(0.0, 2.0), (2.0, 4.0)]


def test_build_cues_times_each_sentence_from_its_words():
    text = "The sword rose. It fell."
    times = [(0.0, 0.3), (0.3, 0.6), (0.6, 1.0), (1.2, 1.4), (1.4, 1.9)]
    cues = build_cues(text, times, with_words=True)
    assert [(c.text, c.start, c.end) for c in cues] == [
        ("The sword rose.", 0.0, 1.0),
        ("It fell.", 1.2, 1.9),
    ]
    assert [w.word for w in cues[1].words] == ["It", "fell."]
    assert build_cues(text, times, with_words=False)[0].words == []


def test_build_cues_rejects_mismatched_word_times():
    with pytest.raises(ValueError):
        build_cues("one two", [(0.0, 1.0)], with_words=False)


def test_refine_cue_times_places_words_inside_the_padded_window():
    audio = np.zeros(2 * SAMPLE_RATE, dtype=np.float32)
    # Coarse times say the cue spans 0.5-1.5 s; the padded window is
    # 0.25-1.75 s. The fake model emits 15 frames of 0.1 s over it: "ab"
    # in frames 3-5 and "c" in frames 8-9.
    frames = [0, 0, 0, 2, 3, 3, 0, 1, 4, 4, 0, 0, 0, 0, 0]

    def emissions(window: np.ndarray) -> np.ndarray:
        assert len(window) == int(1.5 * SAMPLE_RATE)
        return emissions_for(frames)

    refined = refine_cue_times(emissions, audio, "ab c", [(0.5, 1.0), (1.0, 1.5)], VOCAB)
    assert refined[0] == pytest.approx((0.25 + 0.3, 0.25 + 0.6))
    assert refined[1] == pytest.approx((0.25 + 0.8, 0.25 + 1.0))
