import type { PipelineStep, RenderSettings, RenderStage } from "../../api/gen/types.gen";

/** Render step kinds whose output lands in the segment cache (one pin each). */
const CACHEABLE_KINDS = new Set(["render.scene_body", "render.transition", "render.audio_master", "render.subtitles"]);
const TERMINAL = new Set<PipelineStep["status"]>(["done", "failed", "canceled"]);

export type RunGroupKey = "segments" | "audio" | "subtitles" | "compose" | "preview";

export interface RunGroup {
  key: RunGroupKey;
  label: string;
  done: number;
  total: number;
  running: number;
  failed: number;
}

const GROUPS: { key: RunGroupKey; label: string; kinds: string[] }[] = [
  { key: "segments", label: "Scene and transition segments", kinds: ["render.scene_body", "render.transition"] },
  { key: "audio", label: "Narration master", kinds: ["render.audio_master"] },
  { key: "subtitles", label: "Subtitles", kinds: ["render.subtitles"] },
  { key: "compose", label: "Compose and QC", kinds: ["render.compose"] },
  { key: "preview", label: "Previews", kinds: ["render.preview"] },
];

/** Per-stage counters of a render run, computed from its (SSE-patched) steps. */
export function runGroups(steps: PipelineStep[]): RunGroup[] {
  return GROUPS.map(({ key, label, kinds }) => {
    const mine = steps.filter((s) => kinds.includes(s.kind));
    return {
      key,
      label,
      done: mine.filter((s) => s.status === "done").length,
      total: mine.length,
      running: mine.filter((s) => s.status === "running").length,
      failed: mine.filter((s) => s.status === "failed").length,
    };
  }).filter((g) => g.total > 0);
}

/** True once every step of the run has ended (done, failed or canceled). */
export function runFinished(steps: PipelineStep[]): boolean {
  return steps.length > 0 && steps.every((s) => TERMINAL.has(s.status));
}

/**
 * The stage strip from the render status, with the compose and encode
 * stages advanced live from the active run's steps: the status counted the
 * cache when it was fetched, and every cacheable step that finished since
 * adds one more cached entry, so the counter moves without a refetch.
 */
export function liveStages(stages: RenderStage[], steps: PipelineStep[] | undefined): RenderStage[] {
  if (!steps || steps.length === 0) return stages;
  const cacheable = steps.filter((s) => CACHEABLE_KINDS.has(s.kind));
  const doneCacheable = cacheable.filter((s) => s.status === "done").length;
  const compose = steps.find((s) => s.kind === "render.compose");
  return stages.map((st) => {
    if (st.key === "compose" && st.total > 0) {
      const reused = Math.max(0, st.total - cacheable.length);
      const done = Math.min(st.total, Math.max(st.done, reused + doneCacheable));
      return { ...st, done, state: done >= st.total ? "done" : "running" };
    }
    if (st.key === "encode" && compose) {
      if (compose.status === "done") return { ...st, done: 1, total: 1, state: "done" };
      if (compose.status === "running") return { ...st, state: "running" };
    }
    return st;
  });
}

const ENCODER_LABELS: Record<string, string> = { h264_nvenc: "H.264 NVENC", libx264: "H.264 x264", auto: "H.264 auto" };

export function encoderLabel(encoder: string): string {
  return ENCODER_LABELS[encoder] ?? encoder;
}

/** "1920×1080 · 30 fps · H.264 NVENC" */
export function outputSummary(s: Pick<RenderSettings, "width" | "height" | "fps" | "encoder">): string {
  return `${s.width}×${s.height} · ${s.fps} fps · ${encoderLabel(s.encoder)}`;
}

/** Output presets offered by the settings panel; the stored size is kept when it is not one of them. */
export const OUTPUT_SIZES = [
  { width: 1920, height: 1080, label: "1080p" },
  { width: 1280, height: 720, label: "720p" },
  { width: 3840, height: 2160, label: "2160p" },
] as const;

/** m:ss or h:mm:ss from milliseconds. */
export function formatClock(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/** "~4 min" style rough duration from seconds. */
export function formatRoughMinutes(seconds: number): string {
  if (seconds < 60) return "<1 min";
  return `~${Math.round(seconds / 60)} min`;
}

/**
 * Text of a failed request. Generated mutations type their error as the
 * Problem body, while the client interceptor throws an ApiError (an Error)
 * carrying the same detail, so both shapes are handled.
 */
export function errorMessage(error: unknown): string | undefined {
  if (!error) return undefined;
  if (error instanceof Error) return error.message;
  const problem = error as { detail?: string; title?: string };
  return problem.detail ?? problem.title ?? "Request failed.";
}
