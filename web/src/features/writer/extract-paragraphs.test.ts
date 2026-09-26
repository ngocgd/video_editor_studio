import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { afterEach, describe, expect, it } from "vitest";

import { backfillParagraphIds, extractParagraphs } from "./extract-paragraphs";
import { ParagraphWithId } from "./paragraph-with-id-extension";

let editor: Editor | null = null;

function makeEditor(paragraphs: { id: string; text: string }[]): Editor {
  editor = new Editor({
    extensions: [StarterKit.configure({ paragraph: false }), ParagraphWithId],
    content: {
      type: "doc",
      content: paragraphs.map((p) => ({ type: "paragraph", attrs: { id: p.id }, content: [{ type: "text", text: p.text }] })),
    },
    onUpdate: ({ editor: ed }) => backfillParagraphIds(ed),
  });
  return editor;
}

afterEach(() => {
  editor?.destroy();
  editor = null;
});

describe("paragraph ids", () => {
  it("gives the second half of a split paragraph a fresh id", () => {
    const ed = makeEditor([{ id: "p1", text: "Hello world" }]);
    ed.commands.setTextSelection(6); // after "Hello"
    ed.commands.enter();

    const paragraphs = extractParagraphs(ed);
    expect(paragraphs.map((p) => p.text)).toEqual(["Hello", " world"]);
    expect(paragraphs[0].id).toBe("p1");
    expect(paragraphs[1].id).toMatch(/^p_/);
  });

  it("gives an empty paragraph created by Enter at the end its own id", () => {
    const ed = makeEditor([{ id: "p1", text: "Hello" }]);
    ed.commands.setTextSelection(6);
    ed.commands.enter();

    const ids = extractParagraphs(ed).map((p) => p.id);
    expect(ids).toHaveLength(2);
    expect(ids[0]).toBe("p1");
    expect(ids[1]).not.toBe("p1");
    expect(ids[1]).toBeTruthy();
  });

  it("re-ids a duplicated paragraph id, keeping the first occurrence", () => {
    const ed = makeEditor([
      { id: "p1", text: "One" },
      { id: "p1", text: "Copy" },
      { id: "p2", text: "Two" },
    ]);
    backfillParagraphIds(ed);

    const ids = extractParagraphs(ed).map((p) => p.id);
    expect(ids[0]).toBe("p1");
    expect(ids[2]).toBe("p2");
    expect(new Set(ids).size).toBe(3);
  });
});
