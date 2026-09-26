"""Narration text splitting shared by the TTS engines (which synthesize a
bounded amount of text per call) and the align engine (whose subtitle
cues follow sentence boundaries). Works for English and Vietnamese: both
end sentences with . ! ? and the ellipsis, and separate words by spaces.
"""

from __future__ import annotations

import re

# A sentence ends after . ! ? or an ellipsis, optionally followed by one
# closing quote or bracket, which stays with the sentence.
_SENTENCE_END = re.compile(r"(?:(?<=[.!?…])|(?<=[.!?…][\"'”’)\]]))\s+")
_CLAUSE_BREAK = re.compile(r"(?<=[,;:—])\s+")


def count_words(text: str) -> int:
    """Whitespace-delimited word count, the same rule the duration
    estimate uses for EN and VI."""
    return len(text.split())


def split_sentences(text: str) -> list[str]:
    """Splits text into sentences, treating blank lines and line breaks
    as hard boundaries too. Punctuation stays with its sentence."""
    out: list[str] = []
    for block in re.split(r"\n+", text.strip()):
        block = block.strip()
        if not block:
            continue
        out.extend(p.strip() for p in _SENTENCE_END.split(block) if p.strip())
    return out


def _split_long(sentence: str, max_chars: int) -> list[str]:
    """Breaks one over-long sentence at clause punctuation first, then at
    word boundaries, into parts of at most max_chars (a single word longer
    than that stays whole)."""
    parts: list[str] = []
    for clause in _CLAUSE_BREAK.split(sentence):
        if len(clause) <= max_chars:
            parts.append(clause)
            continue
        line: list[str] = []
        for word in clause.split():
            if line and len(" ".join([*line, word])) > max_chars:
                parts.append(" ".join(line))
                line = []
            line.append(word)
        if line:
            parts.append(" ".join(line))
    return _pack(parts, max_chars)


def _pack(pieces: list[str], max_chars: int) -> list[str]:
    """Greedily joins consecutive pieces while they fit in max_chars."""
    out: list[str] = []
    current = ""
    for piece in pieces:
        candidate = f"{current} {piece}" if current else piece
        if current and len(candidate) > max_chars:
            out.append(current)
            current = piece
        else:
            current = candidate
    if current:
        out.append(current)
    return out


def chunk_text(text: str, max_chars: int) -> list[str]:
    """Splits text into chunks of whole sentences of at most max_chars,
    breaking a single long sentence further when it alone is too long."""
    if max_chars <= 0:
        raise ValueError("max_chars must be positive")
    pieces: list[str] = []
    for sentence in split_sentences(text):
        if len(sentence) <= max_chars:
            pieces.append(sentence)
        else:
            pieces.extend(_split_long(sentence, max_chars))
    return _pack(pieces, max_chars)


def subtitle_cues(text: str, max_chars: int = 84) -> list[str]:
    """Subtitle cues: one per sentence, long sentences broken into parts
    of at most max_chars (two lines of about 42 characters)."""
    cues: list[str] = []
    for sentence in split_sentences(text):
        cues.extend([sentence] if len(sentence) <= max_chars else _split_long(sentence, max_chars))
    return cues
