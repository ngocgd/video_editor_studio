import { describe, expect, it } from "vitest";

import { countWords, estimateDurationMinutes, formatDurationEstimate } from "./duration-estimate";

describe("estimateDurationMinutes", () => {
  it("uses 150 wpm for EN", () => {
    expect(estimateDurationMinutes(4500, "en")).toBeCloseTo(30, 5);
  });

  it("uses 165 wpm for VI", () => {
    expect(estimateDurationMinutes(4950, "vi")).toBeCloseTo(30, 5);
  });

  it("returns 0 for an empty or invalid word count", () => {
    expect(estimateDurationMinutes(0, "en")).toBe(0);
    expect(estimateDurationMinutes(Number.NaN, "en")).toBe(0);
    expect(estimateDurationMinutes(-5, "en")).toBe(0);
  });
});

describe("formatDurationEstimate", () => {
  it("labels the estimate as uncalibrated", () => {
    expect(formatDurationEstimate(4500, "en")).toBe("~30 min (uncalibrated)");
  });
});

describe("countWords", () => {
  it("sums whitespace-delimited words across paragraphs", () => {
    expect(countWords([{ text: "one two three" }, { text: "four" }])).toBe(4);
  });

  it("ignores empty paragraphs", () => {
    expect(countWords([{ text: "" }, { text: "  " }, { text: "word" }])).toBe(1);
  });
});
