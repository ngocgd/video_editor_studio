import type { Editor } from "@tiptap/core";

import { newParagraphId } from "./paragraph-with-id-extension";
import type { DiffParagraph } from "./paragraph-diff";

/** Reads the current paragraph array (id + plain text, in document order) out of the TipTap doc. */
export function extractParagraphs(editor: Editor): DiffParagraph[] {
  const paragraphs: DiffParagraph[] = [];
  editor.state.doc.forEach((node) => {
    if (node.type.name === "paragraph") {
      paragraphs.push({ id: (node.attrs.id as string | null) ?? "", text: node.textContent });
    }
  });
  return paragraphs;
}

/**
 * Assigns a fresh id to any paragraph node missing one (e.g. created by
 * paste, not just Enter) in a single transaction. Safe to call from
 * `onUpdate`: once every paragraph has an id this is a no-op, so it cannot
 * loop.
 */
export function backfillParagraphIds(editor: Editor): void {
  const { state } = editor;
  let tr = state.tr;
  let changed = false;
  state.doc.forEach((node, offset) => {
    if (node.type.name === "paragraph" && !node.attrs.id) {
      tr = tr.setNodeMarkup(offset, undefined, { ...node.attrs, id: newParagraphId() });
      changed = true;
    }
  });
  if (changed) editor.view.dispatch(tr);
}
