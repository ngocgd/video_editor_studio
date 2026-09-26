"""CTC forced alignment in numpy: given a wav2vec2 model's per-frame
log-probabilities and the known transcript's token ids, finds the most
likely frame span of every token (Viterbi over the blank-interleaved
label sequence). Implemented here rather than taken from torchaudio,
whose forced_align is deprecated and removed in recent releases, and so
it can be tested without torch.
"""

from __future__ import annotations

import numpy as np

_NEG_INF = -1e30


def forced_align(
    log_probs: np.ndarray, tokens: list[int], blank: int = 0
) -> list[tuple[int, int]] | None:
    """Returns one (start_frame, end_frame_exclusive) per token, or None
    when the audio has too few frames to emit the transcript.

    log_probs is (frames, vocab) log-softmax output. States follow the
    usual CTC topology: blank, t1, blank, t2, ..., tN, blank; a token may
    be skipped into only from the state two back when it differs from
    the previous token.
    """
    frames = log_probs.shape[0]
    n = len(tokens)
    if n == 0:
        return []
    repeats = sum(1 for a, b in zip(tokens, tokens[1:], strict=False) if a == b)
    if frames < n + repeats:
        return None

    labels = np.full(2 * n + 1, blank, dtype=np.int64)
    labels[1::2] = tokens
    states = len(labels)
    # A skip from s-2 is allowed into a token state whose token differs
    # from the token two states back.
    can_skip = np.zeros(states, dtype=bool)
    can_skip[3::2] = labels[3::2] != labels[1:-2:2]

    score = np.full(states, _NEG_INF)
    score[0] = log_probs[0, blank]
    score[1] = log_probs[0, labels[1]]
    back = np.zeros((frames, states), dtype=np.int8)

    for t in range(1, frames):
        stay = score
        step = np.concatenate(([_NEG_INF], score[:-1]))
        skip = np.where(can_skip, np.concatenate(([_NEG_INF, _NEG_INF], score[:-2])), _NEG_INF)
        stacked = np.stack([stay, step, skip])
        choice = np.argmax(stacked, axis=0)
        score = stacked[choice, np.arange(states)] + log_probs[t, labels]
        back[t] = choice

    end = states - 1 if score[states - 1] >= score[states - 2] else states - 2
    if score[end] <= _NEG_INF / 2:
        return None

    path = np.empty(frames, dtype=np.int64)
    s = end
    for t in range(frames - 1, -1, -1):
        path[t] = s
        s -= back[t, s]

    spans: list[tuple[int, int]] = []
    for i in range(n):
        hit = np.nonzero(path == 2 * i + 1)[0]
        if hit.size == 0:
            return None
        spans.append((int(hit[0]), int(hit[-1]) + 1))
    return spans


def align_words(
    log_probs: np.ndarray,
    words: list[str],
    vocab: dict[str, int],
    *,
    blank_token: str = "<pad>",
    delimiter: str = "|",
) -> list[tuple[int, int] | None]:
    """Force-aligns words (the script's own spelling) against log_probs
    from a character-level CTC model (wav2vec2's English vocabulary is
    upper-case letters, the apostrophe and "|" between words). Returns a
    frame span per word, or None for a word with no alignable characters
    (e.g. digits) or when the whole alignment fails."""
    blank = vocab[blank_token]
    sep = vocab.get(delimiter)
    tokens: list[int] = []
    ranges: list[tuple[int, int] | None] = []
    for word in words:
        ids = [vocab[c] for c in word.upper() if c in vocab and c not in (delimiter, blank_token)]
        if not ids:
            ranges.append(None)
            continue
        if tokens and sep is not None:
            tokens.append(sep)
        ranges.append((len(tokens), len(tokens) + len(ids)))
        tokens.extend(ids)
    spans = forced_align(log_probs, tokens, blank=blank)
    if spans is None:
        return [None] * len(words)
    return [None if r is None else (spans[r[0]][0], spans[r[1] - 1][1]) for r in ranges]
