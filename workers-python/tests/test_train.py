"""Tests for LoRA training without ai-toolkit, its weights or a GPU:
parameter validation, the ai-toolkit job config, console parsing and
scrubbing, dataset unpacking, the engine's child-process handling
against a stand-in run.py, and the Train servicer's event stream."""

from __future__ import annotations

import io
import json
import textwrap
import time
import zipfile

import grpc
import pytest

from loomtale_worker import transfer
from loomtale_worker.engines import aitoolkit_train
from loomtale_worker.engines.aitoolkit_train import AiToolkitTrainEngine, child_env
from loomtale_worker.engines.jobs import TrainJob, TrainOutput
from loomtale_worker.engines.registry import EngineRegistry
from loomtale_worker.engines.threaded import InvalidJobError
from loomtale_worker.engines.train_config import (
    bar_position,
    job_config,
    scrub_line,
    train_params,
)
from loomtale_worker.engines.train_dataset import extract_dataset
from loomtale_worker.model_manager import EngineNotInstalledError, ModelManager
from loomtale_worker.servicers.train_service import TrainServicer


def make_zip(entries: dict[str, bytes]) -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        for name, data in entries.items():
            zf.writestr(name, data)
    return buf.getvalue()


def four_refs(**extra: bytes) -> dict[str, bytes]:
    return {**{f"ref-{i}.png": b"png" for i in range(4)}, **extra}


# --- parameters and config ---------------------------------------------------


def test_train_params_defaults_and_bounds():
    p = train_params({})
    assert (p.steps, p.rank, p.learning_rate, p.max_minutes) == (1500, 16, 1e-4, 120)
    assert p.trigger_word == "loomtale_character"
    assert train_params({"steps": "50", "trigger_word": "mira_v1"}).steps == 50
    for bad in ({"steps": "5"}, {"rank": "x"}, {"trigger_word": "Mira!"}, {"max_minutes": "500"}):
        with pytest.raises(InvalidJobError):
            train_params(bad)


def test_job_config_trains_offline_on_the_local_base_without_samples(tmp_path):
    cfg = job_config(
        train_params({"steps": "50", "rank": "8"}),
        dataset_dir=tmp_path / "ds",
        output_dir=tmp_path / "out",
        base_dir=tmp_path / "base",
        adapter=tmp_path / "adapter.safetensors",
    )
    proc = cfg["config"]["process"][0]
    assert proc["model"]["arch"] == "zimage"
    assert proc["model"]["name_or_path"] == str(tmp_path / "base")
    assert proc["model"]["assistant_lora_path"] == str(tmp_path / "adapter.safetensors")
    assert proc["network"] == {"type": "lora", "linear": 8, "linear_alpha": 8}
    assert proc["train"]["steps"] == 50 and proc["train"]["disable_sampling"] is True
    assert proc["save"]["save_every"] == 50
    assert proc["datasets"][0]["folder_path"] == str(tmp_path / "ds")


def test_bar_position_reads_tqdm_bars_only():
    bar = "lora:  12%|#2        | 6/50 [00:06<00:44,  1.00it/s, lr: 1.0e-04 loss: 3.1e-01]"
    assert bar_position(bar) == (6, 50)
    shards = "Loading checkpoint shards:  33%|###3      | 1/3 [00:02<00:04,  2.1s/it]"
    assert bar_position(shards) == (1, 3)
    assert bar_position("Caching latents to disk") is None


def test_scrub_line_removes_urls_tokens_and_escape_codes():
    line = (
        "\x1b[32mfetch http://minio:9000/b/k?X-Amz-Signature=abc token=hf_abcdefghijkl "
        "Authorization: Bearer xyz\x1b[0m"
    )
    clean = scrub_line(line)
    assert "minio" not in clean and "abc" not in clean and "hf_" not in clean
    assert "xyz" not in clean and "\x1b" not in clean
    assert clean.startswith("fetch <url>")
    assert len(scrub_line("x" * 1000)) == 300


# --- dataset -----------------------------------------------------------------


def test_extract_dataset_flattens_names_and_keeps_only_images_and_captions(tmp_path):
    archive = tmp_path / "ds.zip"
    archive.write_bytes(
        make_zip(
            four_refs(
                **{
                    "../../escape.png": b"png",
                    "nested/dir/ref-9.webp": b"webp",
                    "ref-0.txt": b"mira, red scarf",
                    "script.py": b"import os",
                    ".hidden.png": b"png",
                }
            )
        )
    )
    dest = tmp_path / "dataset"
    assert extract_dataset(archive, dest) == 6
    names = sorted(p.name for p in dest.iterdir())
    assert "escape.png" in names and "ref-9.webp" in names and "ref-0.txt" in names
    assert "script.py" not in names and ".hidden.png" not in names
    assert not (tmp_path / "escape.png").exists()


@pytest.mark.parametrize(
    ("entries", "fragment"),
    [
        ({"a.png": b"x", "b.png": b"x"}, "4 to 64 images"),
        (four_refs(**{"x/ref-0.png": b"dup"}), "two entries"),
        (four_refs(**{"ref-0.txt": b"c" * 5000}), "larger than"),
    ],
)
def test_extract_dataset_rejects_unusable_archives(tmp_path, entries, fragment):
    archive = tmp_path / "ds.zip"
    archive.write_bytes(make_zip(entries))
    with pytest.raises(InvalidJobError, match=fragment):
        extract_dataset(archive, tmp_path / "dataset")


def test_extract_dataset_rejects_a_non_zip(tmp_path):
    archive = tmp_path / "ds.zip"
    archive.write_bytes(b"not a zip")
    with pytest.raises(InvalidJobError, match="not a zip"):
        extract_dataset(archive, tmp_path / "dataset")


# --- engine against a stand-in run.py ----------------------------------------

FAKE_RUN_PY = textwrap.dedent(
    """
    import json, os, sys, time
    cfg = json.load(open(sys.argv[1]))
    proc = cfg["config"]["process"][0]
    mode = open(os.path.join(os.path.dirname(__file__), "mode")).read().strip()
    steps = proc["train"]["steps"]
    print("offline=" + os.environ.get("HF_HUB_OFFLINE", ""), flush=True)
    print("leak=" + os.environ.get("LOOMTALE_TEST_SECRET", "none"), flush=True)
    print("fetch https://minio/k?X-Amz-Signature=s3cret", flush=True)
    sys.stdout.write("Loading checkpoint shards:  50%|##   | 1/2 [00:01<00:01]\\r")
    for i in range(1, steps + 1):
        sys.stdout.write(f"lora: |###| {i}/{steps} [00:01<00:01, 1.0it/s]\\r")
        sys.stdout.flush()
    if mode == "hang":
        time.sleep(60)
    if mode == "fail":
        print("CUDA error: something broke", flush=True)
        sys.exit(1)
    out = os.path.join(proc["training_folder"], "lora")
    os.makedirs(out, exist_ok=True)
    open(os.path.join(out, "lora.safetensors"), "wb").write(b"lora-weights")
    """
)


@pytest.fixture
def toolkit(tmp_path, monkeypatch):
    root = tmp_path / "ai-toolkit"
    root.mkdir()
    (root / "run.py").write_text(FAKE_RUN_PY, encoding="utf-8")
    (root / "mode").write_text("ok", encoding="utf-8")
    monkeypatch.setenv("AI_TOOLKIT_DIR", str(root))
    monkeypatch.setenv("LOOMTALE_TEST_SECRET", "do-not-leak")
    monkeypatch.setattr(aitoolkit_train, "RUNTIME_MODULES", ())
    return root


def train_job(tmp_path, *, cancelled=lambda: False, **params) -> tuple[TrainJob, list, list]:
    steps: list[tuple[int, int]] = []
    logs: list[str] = []
    work = tmp_path / "work"
    (work / "dataset").mkdir(parents=True)
    job = TrainJob(
        dataset_dir=work / "dataset",
        work_dir=work,
        base_model="z-image-turbo",
        params={"steps": "20", **params},
        step=lambda s, t: steps.append((s, t)),
        log=logs.append,
        cancelled=cancelled,
    )
    return job, steps, logs


async def test_engine_runs_the_trainer_and_relays_steps_and_scrubbed_logs(tmp_path, toolkit):
    engine = AiToolkitTrainEngine(tmp_path / "models")
    assert await engine.load() == 0
    job, steps, logs = train_job(tmp_path)
    out: TrainOutput = await engine.run(job)
    assert out.weights.read_bytes() == b"lora-weights"
    assert out.metadata["steps"] == "20" and out.metadata["weights_bytes"] == "12"
    assert steps[-1] == (20, 20) and all(total == 20 for _, total in steps)
    assert "offline=1" in logs and "leak=none" in logs
    assert "fetch <url>" in logs
    assert not any("Loading checkpoint" in line or "s3cret" in line for line in logs)
    written = json.loads((tmp_path / "work" / "job.json").read_text())
    assert written["config"]["process"][0]["model"]["arch"] == "zimage"


async def test_engine_failure_carries_no_weights(tmp_path, toolkit):
    (toolkit / "mode").write_text("fail", encoding="utf-8")
    job, _, logs = train_job(tmp_path)
    with pytest.raises(RuntimeError, match="exited with code 1"):
        await AiToolkitTrainEngine(tmp_path / "models").run(job)
    assert "CUDA error: something broke" in logs


async def test_cancelled_job_kills_the_trainer(tmp_path, toolkit):
    (toolkit / "mode").write_text("hang", encoding="utf-8")
    started = time.monotonic()
    flag = {"at": started + 1.5}
    job, _, _ = train_job(tmp_path, cancelled=lambda: time.monotonic() > flag["at"])
    with pytest.raises(RuntimeError, match="cancelled"):
        await AiToolkitTrainEngine(tmp_path / "models").run(job)
    assert time.monotonic() - started < 20


async def test_engine_rejects_another_base_model(tmp_path, toolkit):
    job, _, _ = train_job(tmp_path)
    job.base_model = "qwen-image"
    with pytest.raises(InvalidJobError, match="base_model"):
        await AiToolkitTrainEngine(tmp_path / "models").run(job)


async def test_missing_checkout_is_engine_not_installed(tmp_path, monkeypatch):
    monkeypatch.setenv("AI_TOOLKIT_DIR", str(tmp_path / "absent"))
    engine = AiToolkitTrainEngine(tmp_path)
    for rel in aitoolkit_train.FILES:
        (tmp_path / rel).parent.mkdir(parents=True, exist_ok=True)
        (tmp_path / rel).write_bytes(b"x")
    registry = EngineRegistry()
    registry.register(engine)
    with pytest.raises(EngineNotInstalledError, match="lacks the engine's runtime"):
        await ModelManager(registry).load(engine.name)


def test_child_env_is_an_allowlist(monkeypatch):
    monkeypatch.setenv("PYWORKER_BEARER_TOKEN_PATH", "/run/secrets/x")
    monkeypatch.setenv("HF_TOKEN", "hf_secret")
    env = child_env()
    assert "PYWORKER_BEARER_TOKEN_PATH" not in env and "HF_TOKEN" not in env
    assert env["HF_HUB_OFFLINE"] == "1" and env["TRANSFORMERS_OFFLINE"] == "1"


# --- servicer ----------------------------------------------------------------


class AbortSignal(Exception):
    def __init__(self, code, details):
        super().__init__(details)
        self.code = code


class FakeContext:
    async def abort(self, code, details):
        raise AbortSignal(code, details)


class Request:
    def __init__(self, **kw):
        self.__dict__.update(kw)


class FakeTrainer:
    name = "fake-trainer"
    task = "train"
    license = "MIT"

    def __init__(self, fail: Exception | None = None) -> None:
        self.fail = fail
        self.jobs: list[TrainJob] = []

    def installed(self) -> bool:
        return True

    async def load(self) -> int:
        return 0

    async def unload(self) -> None:
        return None

    async def run(self, job: TrainJob) -> TrainOutput:
        self.jobs.append(job)
        if self.fail:
            raise self.fail
        job.log("Loading ZImage model")
        for step in (1, 2, 2, 4):
            job.step(step, 4)
        weights = job.work_dir / "lora.safetensors"
        weights.write_bytes(b"trained")
        return TrainOutput(weights=weights, metadata={"steps": "4"})


@pytest.fixture
def storage(monkeypatch):
    stored: dict[str, tuple[bytes, str]] = {}
    downloads = {"http://minio/ds": make_zip(four_refs()), "http://minio/bad": b"nope"}
    fetched: list[str] = []

    async def fake_download(url, **_kw):
        fetched.append(url)
        return downloads[url]

    async def fake_upload(url, data, *, content_type="application/octet-stream", **_kw):
        stored[url] = (data, content_type)

    monkeypatch.setattr(transfer, "download", fake_download)
    monkeypatch.setattr(transfer, "upload", fake_upload)
    return stored, fetched


def servicer(engine) -> TrainServicer:
    registry = EngineRegistry()
    registry.register(engine)
    return TrainServicer(ModelManager(registry))


def train_request(**overrides):
    fields = {
        "engine": "fake-trainer",
        "base_model": "z-image-turbo",
        "dataset_get_url": "http://minio/ds",
        "output_put_url": "http://minio/lora",
        "params": {"steps": "50", "output_key": "t/loras/mira.safetensors"},
    }
    fields.update(overrides)
    return Request(**fields)


async def collect(stream):
    return [event async for event in stream]


async def test_train_streams_progress_logs_and_result(storage):
    stored, _ = storage
    engine = FakeTrainer()
    events = await collect(servicer(engine).Train(train_request(), FakeContext()))
    kinds = [e.WhichOneof("event") for e in events]
    assert kinds[-1] == "result" and kinds.count("result") == 1
    progress = [e.progress for e in events if e.WhichOneof("event") == "progress"]
    assert [p.step for p in progress] == [1, 2, 4]
    assert progress[-1].pct == 100 and progress[-1].total_steps == 4
    assert [e.log.line for e in events if e.WhichOneof("event") == "log"] == [
        "Loading ZImage model"
    ]
    result = events[-1].result
    assert result.output_key == "t/loras/mira.safetensors"
    assert result.metadata["images"] == "4" and result.metadata["engine"] == "fake-trainer"
    assert stored["http://minio/lora"] == (b"trained", "application/octet-stream")
    assert not engine.jobs[0].work_dir.exists()


@pytest.mark.parametrize(
    ("overrides", "fragment"),
    [
        ({"dataset_get_url": ""}, "dataset_get_url"),
        ({"output_put_url": ""}, "output_put_url"),
        ({"params": {"steps": "1"}}, "steps"),
        ({"dataset_get_url": "http://minio/bad"}, "not a zip"),
    ],
)
async def test_train_rejects_invalid_requests(storage, overrides, fragment):
    with pytest.raises(AbortSignal, match=fragment) as err:
        await collect(servicer(FakeTrainer()).Train(train_request(**overrides), FakeContext()))
    assert err.value.code == grpc.StatusCode.INVALID_ARGUMENT


async def test_train_without_the_engine_fails_before_downloading(storage):
    _, fetched = storage
    with pytest.raises(AbortSignal) as err:
        await collect(servicer(FakeTrainer()).Train(train_request(engine="nope"), FakeContext()))
    assert err.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert fetched == []


async def test_trainer_failure_is_internal(storage):
    stored, _ = storage
    engine = FakeTrainer(fail=RuntimeError("ai-toolkit exited with code 1"))
    with pytest.raises(AbortSignal) as err:
        await collect(servicer(engine).Train(train_request(), FakeContext()))
    assert err.value.code == grpc.StatusCode.INTERNAL
    assert stored == {}
