import { describe, expect, it } from "vitest";

import { diffParagraphs } from "./paragraph-diff";

describe("diffParagraphs", () => {
  it("produces no ops when nothing changed", () => {
    const saved = [{ id: "a", text: "one" }, { id: "b", text: "two" }];
    expect(diffParagraphs(saved, saved.map((p) => ({ ...p })))).toEqual([]);
  });

  it("emits an upsert for a single changed paragraph only", () => {
    const saved = [{ id: "a", text: "one" }, { id: "b", text: "two" }];
    const current = [{ id: "a", text: "one" }, { id: "b", text: "two-edited" }];
    expect(diffParagraphs(saved, current)).toEqual([{ op: "upsert", paragraphId: "b", text: "two-edited" }]);
  });

  it("emits a delete for a removed paragraph", () => {
    const saved = [{ id: "a", text: "one" }, { id: "b", text: "two" }];
    const current = [{ id: "a", text: "one" }];
    expect(diffParagraphs(saved, current)).toEqual([{ op: "delete", paragraphId: "b" }]);
  });

  it("emits an upsert then a move for a new paragraph inserted in the middle", () => {
    const saved = [{ id: "a", text: "one" }, { id: "b", text: "two" }];
    const current = [{ id: "a", text: "one" }, { id: "new", text: "inserted" }, { id: "b", text: "two" }];
    expect(diffParagraphs(saved, current)).toEqual([
      { op: "upsert", paragraphId: "new", text: "inserted" },
      { op: "move", paragraphId: "new", afterParagraphId: "a" },
    ]);
  });

  it("emits only move ops (no upsert/delete) for a pure reorder, and applying them reproduces the new order", () => {
    const saved = [{ id: "a", text: "1" }, { id: "b", text: "2" }, { id: "c", text: "3" }];
    const current = [{ id: "b", text: "2" }, { id: "a", text: "1" }, { id: "c", text: "3" }];
    const ops = diffParagraphs(saved, current);

    expect(ops.every((op) => op.op === "move")).toBe(true);

    let order = saved.map((p) => p.id);
    for (const op of ops) {
      order = order.filter((id) => id !== op.paragraphId);
      const insertAt = op.afterParagraphId ? order.indexOf(op.afterParagraphId) + 1 : 0;
      order.splice(insertAt, 0, op.paragraphId);
    }
    expect(order).toEqual(current.map((p) => p.id));
  });

  it("does not move a kept paragraph merely because a new paragraph was inserted next to it", () => {
    const saved = [{ id: "a", text: "1" }, { id: "b", text: "2" }];
    const current = [{ id: "a", text: "1" }, { id: "new", text: "x" }, { id: "b", text: "2" }];
    const ops = diffParagraphs(saved, current);
    expect(ops.some((op) => op.paragraphId === "b")).toBe(false);
  });
});
