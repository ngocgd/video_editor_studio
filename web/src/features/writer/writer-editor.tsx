import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useEffect } from "react";

import type { TargetLanguage } from "../../api/gen/types.gen";
import { backfillParagraphIds, extractParagraphs } from "./extract-paragraphs";
import type { DiffParagraph } from "./paragraph-diff";
import { ParagraphWithId } from "./paragraph-with-id-extension";

export interface WriterSelection {
  paragraphIds: string[];
  text: string;
  /** The paragraph holding the caret (or the selection's end), the anchor for Continue. */
  caretParagraphId?: string;
}

/**
 * The TipTap editor pane (phase 6: Literata `read-lg`, max 68ch, `lang="vi"`
 * on VI drafts). Paragraph nodes carry an `id` attribute (see
 * ./paragraph-with-id-extension) so the autosave diff can address them.
 *
 * `initialParagraphs` seeds the document on mount only; the editor owns its
 * doc afterwards. The parent remounts it (via `key`) whenever the document
 * must be replaced: a language switch, a reload after a version conflict,
 * or an accepted AI proposal.
 */
export function WriterEditor({
  initialParagraphs,
  lang,
  readOnly,
  onParagraphsChange,
  onSelectionChange,
}: {
  initialParagraphs: DiffParagraph[];
  lang: TargetLanguage;
  readOnly: boolean;
  onParagraphsChange: (current: DiffParagraph[]) => void;
  onSelectionChange: (selection: WriterSelection) => void;
}) {
  const editor = useEditor({
    extensions: [StarterKit.configure({ paragraph: false }), ParagraphWithId],
    editable: !readOnly,
    content: toDocJson(initialParagraphs),
    onUpdate: ({ editor: ed }) => {
      backfillParagraphIds(ed);
      onParagraphsChange(extractParagraphs(ed));
    },
    onSelectionUpdate: ({ editor: ed }) => {
      const { from, to, empty, $to } = ed.state.selection;
      const caretParagraphId = $to.parent.type.name === "paragraph" ? (($to.parent.attrs.id as string | null) ?? undefined) : undefined;
      if (empty) {
        onSelectionChange({ paragraphIds: [], text: "", caretParagraphId });
        return;
      }
      const ids = new Set<string>();
      ed.state.doc.nodesBetween(from, to, (node) => {
        if (node.type.name === "paragraph" && node.attrs.id) ids.add(node.attrs.id as string);
      });
      onSelectionChange({ paragraphIds: [...ids], text: ed.state.doc.textBetween(from, to, "\n"), caretParagraphId });
    },
  });

  useEffect(() => {
    editor?.setEditable(!readOnly);
  }, [editor, readOnly]);

  return (
    <div className="flex-1 overflow-auto p-6" lang={lang === "vi" ? "vi" : undefined}>
      <EditorContent
        editor={editor}
        className="mx-auto max-w-[68ch] font-serif text-[length:var(--text-read-lg)] leading-[var(--text-read-lg--line-height)] text-foreground focus:outline-none [&_p]:mb-4"
      />
    </div>
  );
}

function toDocJson(paragraphs: DiffParagraph[]) {
  return {
    type: "doc",
    content:
      paragraphs.length > 0
        ? paragraphs.map((p) => ({
            type: "paragraph",
            attrs: { id: p.id },
            content: p.text ? [{ type: "text", text: p.text }] : [],
          }))
        : [{ type: "paragraph", attrs: { id: null } }],
  };
}
