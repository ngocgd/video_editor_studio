import { describe, expect, it } from "vitest";

import type { AnalyticsChannelDay, YouTubeChannel } from "../../api/gen/types.gen";
import {
  evidenceLabel,
  formatDay,
  formatEvidenceValue,
  formatRatioPercent,
  metricCell,
  NOT_AVAILABLE,
  REACH_PENDING,
} from "./analytics-format";
import { analyticsChannels, pickChannel, validateAnalyticsSearch, windowTotals } from "./analytics-selectors";
import { looksLikeVideoRef } from "./track-video-form";
import { retentionSpec, weightedCtr } from "./video-detail-view";

describe("metricCell", () => {
  it("formats a present value, including a real zero", () => {
    expect(metricCell(0, "count", "views")).toEqual({ text: "0", missing: false });
    expect(metricCell(0.0425, "ratioPercent", "ctr", { reachThrough: "2026-09-20" }).text).toBe("4.25%");
  });

  it("labels a metric the API did not return instead of showing 0, with the reason as tooltip", () => {
    const cell = metricCell(undefined, "percent", "averageViewPercentage", { unavailable: { averageViewPercentage: "rejected for this channel" } });
    expect(cell).toEqual({ text: NOT_AVAILABLE, missing: true, reason: "rejected for this channel" });
  });

  it("marks reach metrics pending until the first reach report arrives", () => {
    expect(metricCell(undefined, "count", "impressions").text).toBe(REACH_PENDING);
    expect(metricCell(undefined, "ratioPercent", "ctr", { reachThrough: "2026-09-20" }).text).toBe(NOT_AVAILABLE);
    expect(metricCell(undefined, "count", "views").text).toBe(NOT_AVAILABLE);
  });
});

describe("formatting", () => {
  it("keeps a day on its calendar date whatever the viewer's timezone", () => {
    expect(formatDay("2026-09-25")).toBe("Sep 25, 2026");
    expect(formatDay("garbage")).toBe("garbage");
  });

  it("formats ratios and evidence values", () => {
    expect(formatRatioPercent(0.1)).toBe("10.00%");
    expect(formatEvidenceValue(1234)).toBe("1,234");
    expect(formatEvidenceValue(0.35)).toBe("0.35");
    expect(formatEvidenceValue(["a", 2])).toBe("a, 2");
    expect(formatEvidenceValue(null)).toBe("n/a");
    expect(evidenceLabel("daysSinceLastUpload")).toBe("Days since last upload");
    expect(evidenceLabel("median_ctr")).toBe("Median ctr");
  });
});

describe("selectors", () => {
  const channel = (id: string, status: YouTubeChannel["status"]) => ({ id, status }) as YouTubeChannel;

  it("keeps only a well-formed channel id in the search", () => {
    expect(validateAnalyticsSearch({ channel: "8f4c2d9e-1b3a-4c5d-9e8f-0a1b2c3d4e5f" })).toEqual({ channel: "8f4c2d9e-1b3a-4c5d-9e8f-0a1b2c3d4e5f" });
    expect(validateAnalyticsSearch({ channel: "../etc" })).toEqual({});
  });

  it("drops disconnected channels and falls back to the first channel", () => {
    const list = analyticsChannels([channel("a", "disconnected"), channel("b", "reconnect_needed"), channel("c", "connected")]);
    expect(list.map((c) => c.id)).toEqual(["b", "c"]);
    expect(pickChannel(list, "c")?.id).toBe("c");
    expect(pickChannel(list, "a")?.id).toBe("b");
    expect(pickChannel([], undefined)).toBeUndefined();
  });

  it("sums a window but leaves a metric missing on every day undefined", () => {
    const days: AnalyticsChannelDay[] = [
      { date: "2026-09-01", views: 10, watchHours: 1.5, subscribersGained: 3, subscribersLost: 1, unavailable: {} },
      { date: "2026-09-02", views: 5, subscribersGained: 2, unavailable: { estimatedMinutesWatched: "gap" } },
    ];
    expect(windowTotals(days)).toEqual({ views: 15, watchHours: 1.5, subscribersNet: 4 });
    expect(windowTotals([{ date: "2026-09-01", unavailable: {} }])).toEqual({ views: undefined, watchHours: undefined, subscribersNet: undefined });
  });
});

describe("video detail helpers", () => {
  it("weights CTR by impressions and ignores days without reach data", () => {
    expect(
      weightedCtr([
        { date: "2026-09-01", impressions: 1000, ctr: 0.1, unavailable: {} },
        { date: "2026-09-02", impressions: 9000, ctr: 0.0, unavailable: {} },
        { date: "2026-09-03", views: 4, unavailable: {} },
      ]),
    ).toBeCloseTo(0.01);
    expect(weightedCtr([{ date: "2026-09-03", unavailable: {} }])).toBeUndefined();
  });

  it("adds the relative retention series only when the API returned it", () => {
    expect(retentionSpec([{ elapsedRatio: 0, audienceWatchRatio: 1 }]).series).toHaveLength(1);
    const spec = retentionSpec([{ elapsedRatio: 0.5, audienceWatchRatio: 0.4, relativeRetentionPerformance: 0.6 }, { elapsedRatio: 1 }]);
    expect(spec.series).toHaveLength(2);
    expect(spec.series[0].values).toEqual([0.4, null]);
  });
});

describe("looksLikeVideoRef", () => {
  it("accepts ids and YouTube links, rejects free text", () => {
    expect(looksLikeVideoRef("dQw4w9WgXcQ")).toBe(true);
    expect(looksLikeVideoRef("https://youtu.be/dQw4w9WgXcQ")).toBe(true);
    expect(looksLikeVideoRef("youtube.com/shorts/dQw4w9WgXcQ")).toBe(true);
    expect(looksLikeVideoRef("my video")).toBe(false);
  });
});
