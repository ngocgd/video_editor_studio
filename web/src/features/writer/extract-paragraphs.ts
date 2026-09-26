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
 * Assigns a fresh id to any paragraph node missing one (Enter) or reusing
 * an id already seen earlier in the doc (paste of copied paragraphs), in a
 * single transaction. Ids must be unique: the server rejects a draft with
 * duplicates. Safe to call from `onUpdate`: once every id is present and
 * unique this is a no-op, so it cannot loop.
 */
export function backfillParagraphIds(editor: Editor): void {
  const { state } = editor;
  let tr = state.tr;
  let changed = false;
  const seen = new Set<string>();
  state.doc.forEach((node, offset) => {
    if (node.type.name !== "paragraph") return;
    const id = node.attrs.id as string | null;
    if (id && !seen.has(id)) {
      seen.add(id);
      return;
    }
    tr = tr.setNodeMarkup(offset, undefined, { ...node.attrs, id: newParagraphId() });
    changed = true;
  });
  if (changed) editor.view.dispatch(tr);
}
