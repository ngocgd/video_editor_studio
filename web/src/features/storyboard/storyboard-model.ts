import type { PipKind, PipState, Scene, SceneCounts, SceneFilter, ScenePip } from "../../api/gen/types.gen";
import type { PipelinePip } from "../../components/shared/pipeline-pips";

/** Minimum tile width (px): wide enough for the five pipeline pips in one row. */
export const MIN_TILE_WIDTH = 272;
/** Everything under the 16:9 thumbnail: index row, two narration lines, pips, padding. */
const TILE_CHROME_HEIGHT = 112;

/**
 * Tile height for a tile width: computed, never measured, so the virtual
 * grid has no layout churn; it only changes when the container resizes.
 */
export function tileHeight(tileWidth: number): number {
  return Math.ceil(((tileWidth - 12) * 9) / 16) + TILE_CHROME_HEIGHT;
}
export const GRID_GAP = 8;

/** Columns that fit a container of `width` px, at least one. */
export function columnsForWidth(width: number, minTile = MIN_TILE_WIDTH, gap = GRID_GAP): number {
  if (!Number.isFinite(width) || width <= 0) return 1;
  return Math.max(1, Math.floor((width + gap) / (minTile + gap)));
}

export const PIP_LABELS: Record<PipKind, string> = { text: "TXT", image: "IMG", voice: "VOI", align: "SUB", motion: "MOT" };

const STATE_WORDS: Record<PipState, string> = {
  done: "done",
  running: "running",
  queued: "queued",
  failed: "failed",
  stale: "out of date",
  none: "missing",
};

/** Scene pips in the shape the shared PipelinePips component draws. */
export function toPipelinePips(pips: ScenePip[]): PipelinePip[] {
  return pips.map((p) => ({
    key: p.kind,
    label: PIP_LABELS[p.kind],
    state: p.state,
    detail: p.state === "running" && p.progress ? `${p.progress}%` : undefined,
    staleReason:
      p.state === "stale"
        ? p.staleReason
        : p.state === "failed"
          ? `${PIP_LABELS[p.kind]} failed: ${p.errorMessage || p.errorCode || "unknown error"}`
          : undefined,
  }));
}

/** Screen-reader summary of a scene's pipeline state. */
export function pipSummary(pips: ScenePip[]): string {
  return pips.map((p) => `${PIP_LABELS[p.kind]} ${STATE_WORDS[p.state]}`).join(", ");
}

export const FILTERS: { id: SceneFilter; label: string; count: keyof SceneCounts }[] = [
  { id: "all", label: "All", count: "all" },
  { id: "stale", label: "Stale", count: "stale" },
  { id: "failed", label: "Failed", count: "failed" },
  { id: "missing", label: "Missing", count: "missing" },
  { id: "in_queue", label: "In queue", count: "inQueue" },
];

/** Inclusive index range between an anchor and a clicked index (Shift+Click). */
export function rangeSelection(scenes: Pick<Scene, "id">[], anchor: number, target: number): Set<string> {
  const [from, to] = anchor <= target ? [anchor, target] : [target, anchor];
  return new Set(scenes.slice(Math.max(0, from), to + 1).map((s) => s.id));
}

/** The speaker a scene tile shows: its first speaking character, else the narrator. */
export function firstSpeaker(scene: Scene): string | undefined {
  return scene.segments.find((s) => s.speakerCharacterId)?.speakerCharacterId;
}

export const MOTION_PRESETS = ["ken_burns", "parallax", "static"] as const;
export const MOTION_LABELS: Record<(typeof MOTION_PRESETS)[number], string> = { ken_burns: "Ken Burns", parallax: "Parallax 2.5D", static: "Static" };

/** The next motion preset (the M shortcut cycles through them). */
export function nextMotion(current: Scene["motionPreset"]): Scene["motionPreset"] {
  const i = MOTION_PRESETS.indexOf(current);
  return MOTION_PRESETS[(i + 1) % MOTION_PRESETS.length];
}
