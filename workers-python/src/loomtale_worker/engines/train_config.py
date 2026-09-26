"""Model-free pieces of the ai-toolkit trainer: validated training
parameters, the job config ai-toolkit's run.py reads, and parsing of
its console output into progress steps and scrubbed log lines. Kept
apart from the engine so all of it is unit-tested without a GPU.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from pathlib import Path

from loomtale_worker.engines.threaded import InvalidJobError, float_param, int_param

TRIGGER_WORD = re.compile(r"^[a-z][a-z0-9_]{1,31}$")
DEFAULT_TRIGGER_WORD = "loomtale_character"
RUN_NAME = "lora"


@dataclass(frozen=True)
class TrainParams:
    steps: int
    rank: int
    learning_rate: float
    trigger_word: str
    max_minutes: int


def train_params(params: dict[str, str]) -> TrainParams:
    """Reads the request's params. steps defaults to a character-sized
    run; the smoke benchmark passes 50."""
    trigger = params.get("trigger_word", "") or DEFAULT_TRIGGER_WORD
    if not TRIGGER_WORD.match(trigger):
        raise InvalidJobError(
            "trigger_word must be 2 to 32 lowercase letters, digits or underscores, "
            "starting with a letter"
        )
    return TrainParams(
        steps=int_param(params, "steps", 1500, 10, 4000),
        rank=int_param(params, "rank", 16, 4, 64),
        learning_rate=float_param(params, "learning_rate", 1e-4, 1e-6, 1e-3),
        trigger_word=trigger,
        max_minutes=int_param(params, "max_minutes", 120, 1, 120),
    )


def job_config(
    p: TrainParams, *, dataset_dir: Path, output_dir: Path, base_dir: Path, adapter: Path
) -> dict:
    """The ai-toolkit job for a Z-Image-Turbo character LoRA: quantized
    transformer and text encoder with low_vram so it fits the planned
    budget, the de-distillation adapter merged in for training, no
    sample images (the scorer judges the result) and one final save."""
    return {
        "job": "extension",
        "config": {
            "name": RUN_NAME,
            "process": [
                {
                    "type": "sd_trainer",
                    "training_folder": str(output_dir),
                    "device": "cuda:0",
                    "trigger_word": p.trigger_word,
                    "network": {"type": "lora", "linear": p.rank, "linear_alpha": p.rank},
                    "save": {
                        "dtype": "bf16",
                        "save_every": p.steps,
                        "max_step_saves_to_keep": 1,
                    },
                    "datasets": [
                        {
                            "folder_path": str(dataset_dir),
                            "caption_ext": "txt",
                            "default_caption": p.trigger_word,
                            "caption_dropout_rate": 0.05,
                            "cache_latents_to_disk": True,
                            "resolution": [512, 768, 1024],
                        }
                    ],
                    "train": {
                        "batch_size": 1,
                        "steps": p.steps,
                        "gradient_accumulation": 1,
                        "train_unet": True,
                        "train_text_encoder": False,
                        "gradient_checkpointing": True,
                        "noise_scheduler": "flowmatch",
                        "optimizer": "adamw8bit",
                        "lr": p.learning_rate,
                        "dtype": "bf16",
                        "disable_sampling": True,
                        "skip_first_sample": True,
                    },
                    "model": {
                        "name_or_path": str(base_dir),
                        "arch": "zimage",
                        "assistant_lora_path": str(adapter),
                        "quantize": True,
                        "qtype": "qfloat8",
                        "quantize_te": True,
                        "qtype_te": "qfloat8",
                        "low_vram": True,
                    },
                }
            ],
        },
        "meta": {"name": RUN_NAME, "version": "1.0"},
    }


# tqdm renders "<desc>:  12%|###    | 60/500 [01:00<07:20, 1.00it/s, ...]".
PROGRESS_BAR = re.compile(r"\|\s*(\d+)/(\d+)\s*\[")
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")
URL = re.compile(r"https?://\S+")
TOKEN = re.compile(r"\b(hf_[A-Za-z0-9]{8,}|sk-[A-Za-z0-9_-]{8,})\b")
CREDENTIAL = re.compile(r"(?i)\b(authorization|token|password|secret)(\s*[:=]\s*)(?:bearer\s+)?\S+")
BEARER = re.compile(r"(?i)\b(bearer)(\s+)\S+")
MAX_LINE_CHARS = 300


def bar_position(line: str) -> tuple[int, int] | None:
    """(step, total) of a tqdm progress-bar line, None for other lines.
    The trainer's own bar is the one whose total is the job's steps;
    loading and caching bars are console noise, never log lines."""
    matches = PROGRESS_BAR.findall(line)
    if not matches:
        return None
    step, total = matches[-1]
    return int(step), int(total)


def scrub_line(line: str) -> str:
    """A console line safe to stream to the UI: no escape codes, no URLs
    (presigned URLs carry signatures), no tokens or credentials, and a
    bounded length."""
    line = ANSI.sub("", line)
    line = URL.sub("<url>", line)
    line = TOKEN.sub("<redacted>", line)
    for pattern in (CREDENTIAL, BEARER):
        line = pattern.sub(lambda m: f"{m.group(1)}{m.group(2)}<redacted>", line)
    line = "".join(ch for ch in line if ch.isprintable()).strip()
    if len(line) > MAX_LINE_CHARS:
        line = line[: MAX_LINE_CHARS - 3] + "..."
    return line
