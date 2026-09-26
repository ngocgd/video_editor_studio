import { describe, expect, it } from "vitest";

import { diffText } from "./text-diff";

function rebuild(segments: { text: string }[]): string {
  return segments.map((s) => s.text).join("");
}

describe("diffText", () => {
  it("returns a single keep segment for identical text", () => {
    const segments = diffText("hello world", "hello world");
    expect(segments).toHaveLength(1);
    expect(segments[0].op).toBe("keep");
  });

  it("marks a changed word as remove + insert", () => {
    const segments = diffText("the fire burned", "the fire raged");
    expect(segments.some((s) => s.op === "remove" && s.text.includes("burned"))).toBe(true);
    expect(segments.some((s) => s.op === "insert" && s.text.includes("raged"))).toBe(true);
  });

  it("reconstructs the proposed text from keep+insert segments", () => {
    const original = "Lin Mo sat very still.";
    const proposed = "Lin Mo sat perfectly still, afraid.";
    const segments = diffText(original, proposed);
    const reconstructed = segments.filter((s) => s.op !== "remove").map((s) => s.text).join("");
    expect(reconstructed).toBe(proposed);
  });

  it("handles a pure insertion (continue)", () => {
    const segments = diffText("", "A bell rang.");
    expect(segments).toEqual([{ op: "insert", text: "A bell rang." }]);
    expect(rebuild(segments.filter((s) => s.op !== "remove"))).toBe("A bell rang.");
  });
});
