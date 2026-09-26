/** A clip on the timeline: one scene's span in episode time. */
export interface Clip {
  id: string;
  startMs: number;
  durationMs: number;
}

/** Zoom bounds, in pixels per second. */
export const MIN_PX_PER_S = 0.05;
export const MAX_PX_PER_S = 200;

/** Above this visible span the waveform is not fetched (too many chunks). */
export const WAVEFORM_MAX_VISIBLE_MS = 10 * 60 * 1000;

export function clampZoom(pxPerS: number): number {
  return Math.min(MAX_PX_PER_S, Math.max(MIN_PX_PER_S, pxPerS));
}

/** Index of the first clip that ends after `ms` (clips sorted by start). */
export function firstVisibleIndex(clips: Clip[], ms: number): number {
  let lo = 0;
  let hi = clips.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (clips[mid].startMs + clips[mid].durationMs <= ms) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/** The clips overlapping [startMs, endMs), found by binary search so a 3h timeline costs O(log n + visible). */
export function visibleClips(clips: Clip[], startMs: number, endMs: number): { from: number; to: number } {
  const from = firstVisibleIndex(clips, startMs);
  let to = from;
  while (to < clips.length && clips[to].startMs < endMs) to += 1;
  return { from, to };
}

/** Keeps the view start inside [0, total - visible]. */
export function clampViewStart(viewStartMs: number, visibleMs: number, totalMs: number): number {
  return Math.max(0, Math.min(viewStartMs, Math.max(0, totalMs - visibleMs)));
}

/** Zooms by `factor` keeping the time under `anchorPx` fixed. */
export function zoomAround(viewStartMs: number, pxPerS: number, factor: number, anchorPx: number): { viewStartMs: number; pxPerS: number } {
  const next = clampZoom(pxPerS * factor);
  const anchorMs = viewStartMs + (anchorPx / pxPerS) * 1000;
  return { pxPerS: next, viewStartMs: Math.max(0, anchorMs - (anchorPx / next) * 1000) };
}

/** Picks round ruler steps (in ms) so labels are at least `minPx` apart. */
export function rulerStepMs(pxPerS: number, minPx = 80): number {
  const steps = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600].map((s) => s * 1000);
  for (const step of steps) {
    if ((step / 1000) * pxPerS >= minPx) return step;
  }
  return steps[steps.length - 1];
}
