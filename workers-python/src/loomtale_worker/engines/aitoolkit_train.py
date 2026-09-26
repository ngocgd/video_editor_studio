"""Character LoRA training on the scene model (Z-Image-Turbo) with
ai-toolkit (MIT, github.com/ostris/ai-toolkit), hosted by this worker
like every other engine: the ModelManager holds its single GPU slot for
the whole run, so no other engine is resident while it trains.

ai-toolkit is not a pip package; the worker image built with the train
extra carries a checkout pinned to one commit at AI_TOOLKIT_DIR. Each
job runs its run.py in a child process of this worker (same container,
same offline network, same memory limit) so a crash or a cancelled RPC
ends with the process killed and all of its VRAM returned. The child
gets an allowlisted environment: no credentials, HF offline.
"""

from __future__ import annotations

import importlib.util
import json
import os
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path

from loomtale_worker.engines.jobs import TrainJob, TrainOutput
from loomtale_worker.engines.local_files import LocalFiles
from loomtale_worker.engines.threaded import InvalidJobError, ThreadedEngine
from loomtale_worker.engines.train_config import (
    RUN_NAME,
    bar_position,
    job_config,
    scrub_line,
    train_params,
)

BASE_MODEL = "z-image-turbo"
WEIGHTS_DIR = "train/z-image-turbo"
ADAPTER = f"{WEIGHTS_DIR}/training-adapter/zimage_turbo_training_adapter_v2.safetensors"
_TE = "text_encoder/model-0000{}-of-00003.safetensors"
_DIT = "transformer/diffusion_pytorch_model-0000{}-of-00003.safetensors"
FILES = (
    f"{WEIGHTS_DIR}/model_index.json",
    f"{WEIGHTS_DIR}/scheduler/scheduler_config.json",
    f"{WEIGHTS_DIR}/text_encoder/config.json",
    f"{WEIGHTS_DIR}/text_encoder/generation_config.json",
    *(f"{WEIGHTS_DIR}/{_TE.format(i)}" for i in (1, 2, 3)),
    f"{WEIGHTS_DIR}/text_encoder/model.safetensors.index.json",
    f"{WEIGHTS_DIR}/tokenizer/merges.txt",
    f"{WEIGHTS_DIR}/tokenizer/tokenizer.json",
    f"{WEIGHTS_DIR}/tokenizer/tokenizer_config.json",
    f"{WEIGHTS_DIR}/tokenizer/vocab.json",
    f"{WEIGHTS_DIR}/transformer/config.json",
    *(f"{WEIGHTS_DIR}/{_DIT.format(i)}" for i in (1, 2, 3)),
    f"{WEIGHTS_DIR}/transformer/diffusion_pytorch_model.safetensors.index.json",
    f"{WEIGHTS_DIR}/vae/config.json",
    f"{WEIGHTS_DIR}/vae/diffusion_pytorch_model.safetensors",
    ADAPTER,
)

DEFAULT_AI_TOOLKIT_DIR = "/opt/ai-toolkit"
# Imported by ai-toolkit itself; checked (not imported) at load so an
# image without the train extra answers engine_not_installed.
RUNTIME_MODULES = ("torch", "diffusers", "transformers", "optimum.quanto")
# Variables the trainer may inherit; everything else (tokens, the
# worker's secrets paths, proxies) stays out of the child.
ENV_ALLOWLIST = (
    "PATH",
    "HOME",
    "LANG",
    "LC_ALL",
    "TZ",
    "LD_LIBRARY_PATH",
    "CUDA_VISIBLE_DEVICES",
    "NVIDIA_VISIBLE_DEVICES",
    "NVIDIA_DRIVER_CAPABILITIES",
    "PYTORCH_CUDA_ALLOC_CONF",
    "HF_HUB_CACHE",
)
MAX_LOG_LINES = 2000
POLL_S = 1.0


def ai_toolkit_dir() -> Path:
    return Path(os.environ.get("AI_TOOLKIT_DIR", DEFAULT_AI_TOOLKIT_DIR))


def child_env() -> dict[str, str]:
    env = {k: os.environ[k] for k in ENV_ALLOWLIST if k in os.environ}
    env.update(
        HF_HUB_OFFLINE="1",
        TRANSFORMERS_OFFLINE="1",
        HF_HUB_DISABLE_TELEMETRY="1",
        DISABLE_TELEMETRY="1",
        PYTHONUNBUFFERED="1",
    )
    return env


class AiToolkitTrainEngine(ThreadedEngine):
    name = "z-image-turbo-trainer"
    task = "train"
    license = "MIT"

    def __init__(self, models_root: Path) -> None:
        self.files = LocalFiles(models_root, FILES)

    def _load_sync(self) -> None:
        # Nothing is held between jobs: the child process owns the VRAM
        # while it trains. Loading only proves the runtime is present.
        run_py = ai_toolkit_dir() / "run.py"
        if not run_py.is_file():
            raise ImportError(f"ai-toolkit checkout ({run_py} missing)", name="ai-toolkit")
        for module in RUNTIME_MODULES:
            if importlib.util.find_spec(module.split(".")[0]) is None:
                raise ImportError(f"module {module} not installed", name=module)

    def _unload_sync(self) -> None:
        return None

    def _run_sync(self, job: TrainJob) -> TrainOutput:
        if job.base_model not in ("", BASE_MODEL):
            raise InvalidJobError(f"base_model must be {BASE_MODEL!r}, got {job.base_model!r}")
        p = train_params(job.params)
        output_dir = job.work_dir / "output"
        config_path = job.work_dir / "job.json"
        config = job_config(
            p,
            dataset_dir=job.dataset_dir,
            output_dir=output_dir,
            base_dir=self.files.root / WEIGHTS_DIR,
            adapter=self.files.path(ADAPTER),
        )
        config_path.write_text(json.dumps(config, indent=2), encoding="utf-8")

        started = time.monotonic()
        code = self._run_trainer(job, config_path, p.steps, p.max_minutes * 60)
        elapsed = time.monotonic() - started
        weights = output_dir / RUN_NAME / f"{RUN_NAME}.safetensors"
        if code != 0 or not weights.is_file():
            raise RuntimeError(f"ai-toolkit exited with code {code} and no final weights")
        return TrainOutput(
            weights=weights,
            metadata={
                "base_model": BASE_MODEL,
                "steps": str(p.steps),
                "rank": str(p.rank),
                "learning_rate": f"{p.learning_rate:g}",
                "trigger_word": p.trigger_word,
                "train_s": f"{elapsed:.1f}",
                "weights_bytes": str(weights.stat().st_size),
            },
        )

    def _run_trainer(self, job: TrainJob, config: Path, steps: int, max_s: int) -> int:
        """Runs run.py, relaying its output until it exits. Kills the
        whole process group on cancellation or when max_s is exceeded."""
        proc = subprocess.Popen(  # noqa: S603 - fixed argv, no shell
            [sys.executable, "run.py", str(config)],
            cwd=ai_toolkit_dir(),
            env=child_env(),
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        reader = threading.Thread(target=self._relay, args=(proc, job, steps), daemon=True)
        reader.start()
        deadline = time.monotonic() + max_s
        try:
            while True:
                try:
                    code = proc.wait(timeout=POLL_S)
                    break
                except subprocess.TimeoutExpired:
                    pass
                if job.cancelled():
                    _kill(proc)
                    raise RuntimeError("training cancelled")
                if time.monotonic() > deadline:
                    _kill(proc)
                    raise RuntimeError(f"training exceeded {max_s // 60} minutes")
        except BaseException:
            _kill(proc)
            raise
        finally:
            reader.join(timeout=5)
        return code

    @staticmethod
    def _relay(proc: subprocess.Popen, job: TrainJob, steps: int) -> None:
        """Splits the child's output on newlines and carriage returns
        (tqdm redraws with \\r): the training bar becomes step events,
        other lines become scrubbed log lines (bounded in number)."""
        assert proc.stdout is not None
        pending = b""
        logged = 0
        for chunk in iter(lambda: proc.stdout.read1(4096), b""):
            parts = (pending + chunk).replace(b"\r", b"\n").split(b"\n")
            pending = parts.pop()
            for raw in parts:
                line = raw.decode("utf-8", errors="replace")
                bar = bar_position(line)
                if bar is not None:
                    if bar[1] == steps:
                        job.step(bar[0], steps)
                    continue
                clean = scrub_line(line)
                if clean and logged < MAX_LOG_LINES:
                    logged += 1
                    job.log(clean)
        if pending:
            clean = scrub_line(pending.decode("utf-8", errors="replace"))
            if clean and logged < MAX_LOG_LINES:
                job.log(clean)


def _kill(proc: subprocess.Popen) -> None:
    if proc.poll() is not None:
        return
    try:
        if hasattr(os, "killpg"):
            os.killpg(proc.pid, signal.SIGKILL)
        else:
            proc.kill()
    except ProcessLookupError:
        return
    proc.wait()
