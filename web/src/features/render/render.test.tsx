import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import type { PipelineStep, RenderSettings, RenderStage } from "../../api/gen/types.gen";
import { DiskChip } from "./disk-chip";
import { errorMessage, formatClock, liveStages, outputSummary, runFinished, runGroups } from "./render-model";
import { RenderSettingsPanel } from "./render-settings-panel";
import { RenderStageStrip } from "./render-stage-strip";

function step(kind: string, status: PipelineStep["status"], id = `${kind}-${status}-${Math.random()}`): PipelineStep {
  return { id, runId: "r", scopeKind: "render_manifest", scopeId: "m", kind, queue: "render", priority: 0, status, attempt: 1, version: 1, remainingDeps: 0, progress: 0, createdAt: "2026-09-26T00:00:00Z" };
}

const SETTINGS: RenderSettings = {
  width: 1920,
  height: 1080,
  fps: 30,
  encoder: "auto",
  subtitles: "burn",
  subtitleStyle: { font: "Literata", sizePx: 42, position: "bottom", shadowPx: 2 },
  defaultMotion: "ken_burns",
  crossfadeMs: 600,
  loudnessLufs: -14,
  truePeakDbtp: -1,
};

const STAGES: RenderStage[] = [
  { key: "script", label: "Script", done: 1, total: 1, state: "done" },
  { key: "scenes", label: "Scenes", done: 4, total: 4, state: "done" },
  { key: "images", label: "Images", done: 4, total: 4, state: "done" },
  { key: "voice", label: "Voice", done: 4, total: 4, state: "done" },
  { key: "subtitles", label: "Subtitles", done: 4, total: 4, state: "done" },
  { key: "compose", label: "Compose", done: 3, total: 9, state: "running" },
  { key: "encode", label: "Encode", done: 0, total: 1, state: "running" },
];

describe("render run counters", () => {
  it("groups the run's steps by render stage", () => {
    const steps = [
      step("render.scene_body", "done"),
      step("render.scene_body", "running"),
      step("render.transition", "failed"),
      step("render.audio_master", "done"),
      step("render.compose", "pending"),
      step("render.preview", "pending"),
    ];
    const groups = runGroups(steps);
    expect(groups.map((g) => [g.key, g.done, g.total, g.running, g.failed])).toEqual([
      ["segments", 1, 3, 1, 1],
      ["audio", 1, 1, 0, 0],
      ["compose", 0, 1, 0, 0],
      ["preview", 0, 1, 0, 0],
    ]);
    expect(runFinished(steps)).toBe(false);
    expect(runFinished(steps.map((s) => ({ ...s, status: "done" as const })))).toBe(true);
    expect(runFinished([])).toBe(false);
  });

  it("advances the compose stage from finished cacheable steps without a refetch", () => {
    // 9 pins, 5 cacheable steps enqueued: 4 were reused from the cache.
    const steps = [
      step("render.scene_body", "done"),
      step("render.scene_body", "done"),
      step("render.transition", "running"),
      step("render.audio_master", "queued"),
      step("render.subtitles", "done"),
      step("render.compose", "pending"),
    ];
    const compose = liveStages(STAGES, steps).find((s) => s.key === "compose");
    expect(compose).toMatchObject({ done: 7, total: 9, state: "running" });
  });

  it("marks encode done once compose finishes", () => {
    const steps = [step("render.scene_body", "done"), step("render.compose", "done")];
    const encode = liveStages(STAGES, steps).find((s) => s.key === "encode");
    expect(encode).toMatchObject({ done: 1, total: 1, state: "done" });
    expect(liveStages(STAGES, undefined)).toBe(STAGES);
  });
});

describe("formatting", () => {
  it("formats clocks, outputs and errors", () => {
    expect(formatClock(5_200)).toBe("0:05");
    expect(formatClock(2_292_000)).toBe("38:12");
    expect(formatClock(3_725_000)).toBe("1:02:05");
    expect(outputSummary({ width: 1920, height: 1080, fps: 30, encoder: "h264_nvenc" })).toBe("1920×1080 · 30 fps · H.264 NVENC");
    expect(errorMessage(new Error("boom"))).toBe("boom");
    expect(errorMessage({ title: "Conflict", status: 409, detail: "already running" })).toBe("already running");
    expect(errorMessage(null)).toBeUndefined();
  });
});

describe("RenderStageStrip", () => {
  it("shows every stage with its counters and is accessible", async () => {
    const { container } = render(<RenderStageStrip stages={STAGES} encoder="libx264" />);
    expect(screen.getByLabelText("Compose: running")).toHaveTextContent("3/9 cached");
    expect(screen.getByLabelText("Encode: running")).toHaveTextContent("libx264");
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe("DiskChip", () => {
  it("is hidden while space is fine and warns otherwise", () => {
    const disk = { level: "ok" as const, freeBytes: 100e9, minFreeBytes: 40e9, warnFreeBytes: 60e9, message: "" };
    const { container, rerender } = render(<DiskChip disk={disk} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<DiskChip disk={{ ...disk, level: "blocked", freeBytes: 30 * 1024 ** 3, message: "Below 40 GB" }} />);
    expect(screen.getByTitle("Below 40 GB")).toHaveTextContent("30.0 GB free");
  });
});

describe("RenderSettingsPanel", () => {
  function panel(onSave = vi.fn()) {
    return render(
      <RenderSettingsPanel
        episodeTitle="Ep 07"
        settings={SETTINGS}
        langs={["en", "vi"]}
        lang="en"
        onLangChange={vi.fn()}
        estimate={{ durationMs: 2_292_000, scenes: 48, segments: 95, cachedSegments: 40, encodeSeconds: 240, encoder: "libx264" }}
        saving={false}
        onSave={onSave}
      />,
    );
  }

  it("states that there is no background music and shows the estimate", () => {
    panel();
    expect(screen.getByText("No background music: narration only, by project decision.")).toBeInTheDocument();
    expect(screen.getByText("38:12")).toBeInTheDocument();
    expect(screen.getByText("~4 min")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Parallax (depth model not installed)" })).toBeDisabled();
  });

  it("saves only after a change, with the edited value", () => {
    const onSave = vi.fn();
    panel(onSave);
    const save = screen.getByRole("button", { name: "Save settings" });
    expect(save).toBeDisabled();
    fireEvent.click(screen.getByRole("radio", { name: "SRT only" }));
    expect(save).toBeEnabled();
    fireEvent.click(save);
    expect(onSave).toHaveBeenCalledWith({ ...SETTINGS, subtitles: "srt" });
  });
});
