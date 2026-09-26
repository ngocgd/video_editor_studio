import { describe, expect, it } from "vitest";

import { clampViewStart, type Clip, clampZoom, MAX_PX_PER_S, rulerStepMs, visibleClips, zoomAround } from "./timeline-math";

function clips(n: number, ms = 27_000): Clip[] {
  return Array.from({ length: n }, (_, i) => ({ id: `c${i}`, startMs: i * ms, durationMs: ms }));
}

describe("timeline math", () => {
  it("finds only the clips in view for a 3-hour, 400-clip timeline", () => {
    const all = clips(400);
    expect(all[399].startMs + all[399].durationMs).toBe(10_800_000);
    const { from, to } = visibleClips(all, 3_600_000, 3_600_000 + 120_000);
    expect(from).toBe(133);
    expect(to - from).toBe(5);
    expect(visibleClips(all, 0, 1)).toEqual({ from: 0, to: 1 });
    expect(visibleClips(all, 20_000_000, 20_100_000)).toEqual({ from: 400, to: 400 });
  });

  it("zooms around the pointer and clamps", () => {
    const next = zoomAround(60_000, 10, 2, 100);
    // The time under x=100 (70s) stays under x=100 at the new zoom.
    expect(next.pxPerS).toBe(20);
    expect(next.viewStartMs + (100 / next.pxPerS) * 1000).toBeCloseTo(70_000);
    expect(clampZoom(1e9)).toBe(MAX_PX_PER_S);
    expect(clampViewStart(-5, 1000, 10_000)).toBe(0);
    expect(clampViewStart(9_500, 1000, 10_000)).toBe(9_000);
  });

  it("keeps ruler labels apart", () => {
    expect(rulerStepMs(100)).toBe(1000);
    expect(rulerStepMs(10)).toBe(10_000);
    expect(rulerStepMs(0.1)).toBe(900_000);
  });
});
