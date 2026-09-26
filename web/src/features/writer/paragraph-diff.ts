import type { ParagraphOp } from "../../api/gen/types.gen";

export interface DiffParagraph {
  id: string;
  text: string;
}

/**
 * Diffs the editor's current paragraph array against the last-saved one and
 * produces the minimal `upsert`/`delete`/`move` ops for `PATCH
 * /episodes/{id}/drafts/{lang}` (phase 6 autosave, budget: only changed
 * paragraphs go over the wire, keeping a single-paragraph edit well under
 * the 5KB payload cap).
 *
 * Move detection only compares a kept paragraph's nearest *kept* neighbor
 * (skipping newly-inserted paragraphs, which already carry their own move
 * op) against its saved-order neighbor, so a plain text edit with no
 * reordering never emits a move op, and a paragraph inserted next to
 * otherwise-unchanged siblings does not spuriously "move" them too.
 */
export function diffParagraphs(saved: DiffParagraph[], current: DiffParagraph[]): ParagraphOp[] {
  const savedById = new Map(saved.map((p) => [p.id, p.text]));
  const currentIds = new Set(current.map((p) => p.id));

  const ops: ParagraphOp[] = [];

  for (const p of saved) {
    if (!currentIds.has(p.id)) {
      ops.push({ op: "delete", paragraphId: p.id });
    }
  }

  for (const p of current) {
    const savedText = savedById.get(p.id);
    if (savedText === undefined || savedText !== p.text) {
      ops.push({ op: "upsert", paragraphId: p.id, text: p.text });
    }
  }

  const survivingSavedOrder = saved.map((p) => p.id).filter((id) => currentIds.has(id));
  const expectedPrevKept = new Map<string, string>();
  survivingSavedOrder.forEach((id, index) => {
    expectedPrevKept.set(id, index === 0 ? "" : survivingSavedOrder[index - 1]);
  });

  current.forEach((p, index) => {
    const isNew = !savedById.has(p.id);
    const actualPrev = index === 0 ? "" : current[index - 1].id;

    if (isNew) {
      ops.push({ op: "move", paragraphId: p.id, afterParagraphId: actualPrev });
      return;
    }

    let actualPrevKept = "";
    for (let i = index - 1; i >= 0; i -= 1) {
      if (savedById.has(current[i].id)) {
        actualPrevKept = current[i].id;
        break;
      }
    }
    if (actualPrevKept !== (expectedPrevKept.get(p.id) ?? "")) {
      ops.push({ op: "move", paragraphId: p.id, afterParagraphId: actualPrev });
    }
  });

  return ops;
}
