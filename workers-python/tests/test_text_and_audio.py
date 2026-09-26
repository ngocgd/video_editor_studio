"""Unit tests for narration text splitting and WAV helpers."""

from __future__ import annotations

import numpy as np
import pytest

from loomtale_worker.engines.audio import join_with_pauses, read_wav, to_wav_bytes
from loomtale_worker.engines.text_chunks import (
    chunk_text,
    count_words,
    split_sentences,
    subtitle_cues,
)


def test_split_sentences_keeps_punctuation_and_closing_quotes():
    text = 'He drew the sword. "Run!" she cried. Why?\nThe mountain fell silent… Then thunder.'
    assert split_sentences(text) == [
        "He drew the sword.",
        '"Run!"',
        "she cried.",
        "Why?",
        "The mountain fell silent…",
        "Then thunder.",
    ]


def test_split_sentences_handles_vietnamese():
    text = "Trời đã tối. Anh ấy rút kiếm ra! Cô ấy hỏi: tại sao?"
    assert split_sentences(text) == ["Trời đã tối.", "Anh ấy rút kiếm ra!", "Cô ấy hỏi: tại sao?"]


def test_chunk_text_packs_whole_sentences_under_the_limit():
    text = "One two. Three four. Five six seven."
    assert chunk_text(text, 20) == ["One two. Three four.", "Five six seven."]


def test_chunk_text_breaks_an_overlong_sentence_at_clauses_then_words():
    sentence = "alpha beta gamma, delta epsilon zeta eta theta iota kappa lambda."
    chunks = chunk_text(sentence, 24)
    assert all(len(c) <= 24 for c in chunks)
    assert " ".join(chunks).split() == sentence.split()


def test_chunking_never_loses_or_reorders_words():
    text = (
        "Mây trắng bay. " * 40 + "A very long closing sentence without any punctuation at all " * 5
    )
    for limit in (30, 84, 280, 1000):
        assert " ".join(chunk_text(text, limit)).split() == text.split()
        assert " ".join(subtitle_cues(text, limit)).split() == text.split()


def test_chunk_text_rejects_a_non_positive_limit():
    with pytest.raises(ValueError):
        chunk_text("x", 0)


def test_count_words_is_whitespace_based():
    assert count_words("  Trời   đã tối.\nAnh ấy ") == 5


def test_wav_round_trip_and_pauses():
    rate = 24_000
    tone = np.sin(np.linspace(0, 2 * np.pi * 440, rate)).astype(np.float32) * 0.5
    joined = join_with_pauses([tone, tone], rate, 0.25)
    assert len(joined) == 2 * rate + rate // 4
    samples, got_rate = read_wav(to_wav_bytes(joined, rate))
    assert got_rate == rate
    assert len(samples) == len(joined)
    assert np.max(np.abs(samples - joined)) < 1e-3


def test_to_wav_clips_out_of_range_samples():
    samples, _ = read_wav(to_wav_bytes(np.array([2.0, -2.0], dtype=np.float32), 16_000))
    assert samples[0] == pytest.approx(32767 / 32768)
    assert samples[1] == pytest.approx(-32767 / 32768)
