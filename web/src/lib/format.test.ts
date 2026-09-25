import { describe, expect, it } from "vitest";

import { formatBytes, formatEta, formatMb, formatPercent, formatTimecode } from "./format";

describe("formatTimecode", () => {
  it("formats seconds under an hour as MM:SS", () => {
    expect(formatTimecode(65)).toBe("01:05");
  });

  it("formats an hour or more as H:MM:SS", () => {
    expect(formatTimecode(3665)).toBe("1:01:05");
  });

  it("clamps negative or non-finite input to zero", () => {
    expect(formatTimecode(-5)).toBe("00:00");
    expect(formatTimecode(NaN)).toBe("00:00");
  });
});

describe("formatEta", () => {
  it("returns undefined for missing or negative values", () => {
    expect(formatEta(undefined)).toBeUndefined();
    expect(formatEta(null)).toBeUndefined();
    expect(formatEta(-1)).toBeUndefined();
  });

  it("prefixes the timecode with ETA", () => {
    expect(formatEta(134)).toBe("ETA 02:14");
  });
});

describe("formatBytes", () => {
  it("formats zero and sub-byte values as 0 B", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats kilobytes and megabytes with one decimal", () => {
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1024 * 1024 * 2.5)).toBe("2.5 MB");
  });
});

describe("formatMb", () => {
  it("delegates to formatBytes after converting megabytes to bytes", () => {
    expect(formatMb(16384)).toBe(formatBytes(16384 * 1024 * 1024));
  });
});

describe("formatPercent", () => {
  it("rounds to the nearest whole number", () => {
    expect(formatPercent(43.4)).toBe("43%");
    expect(formatPercent(43.5)).toBe("44%");
  });
});
