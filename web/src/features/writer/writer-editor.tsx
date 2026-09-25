import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useEffect, useRef } from "react";

import type { TargetLanguage } from "../../api/gen/types.gen";
import { backfillParagraphIds, extractParagraphs } from "./extract-paragraphs";
import type { DiffParagraph } from "./paragraph-diff";
import { ParagraphWithId } from "./paragraph-with-id-extension";

export interface WriterSelection {
  paragraphIds: string[];
  text: string;
}

/**
 * The TipTap editor pane (phase 6: Literata `read-lg`, max 68ch, `lang="vi"`
 * on VI drafts). Paragraph nodes carry an `id` attribute (see
 * ./paragraph-with-id-extension) so the autosave diff can address them.
 */
export function WriterEditor({
  paragraphs,
  lang,
  readOnly,
  onParagraphsChange,
  onSelectionChange,
}: {
  paragraphs: DiffParagraph[];
  lang: TargetLanguage;
  readOnly: boolean;
  onParagraphsChange: (current: DiffParagraph[]) => void;
  onSelectionChange: (selection: WriterSelection) => void;
}) {
  const loadedForLang = useRef<TargetLanguage | null>(null);

  const editor = useEditor({
    extensions: [StarterKit.configure({ paragraph: false }), ParagraphWithId],
    editable: !readOnly,
    content: toDocJson(paragraphs),
    onUpdate: ({ editor: ed }) => {
      backfillParagraphIds(ed);
      onParagraphsChange(extractParagraphs(ed));
    },
    onSelectionUpdate: ({ editor: ed }) => {
      const { from, to, empty } = ed.state.selection;
      if (empty) {
        onSelectionChange({ paragraphIds: [], text: "" });
        return;
      }
      const ids = new Set<string>();
      ed.state.doc.nodesBetween(from, to, (node) => {
        if (node.type.name === "paragraph" && node.attrs.id) ids.add(node.attrs.id as string);
      });
      onSelectionChange({ paragraphIds: [...ids], text: ed.state.doc.textBetween(from, to, "\n") });
    },
  });

  // Reload the doc when the lang toggle swaps drafts (a fresh paragraph array from a different query).
  useEffect(() => {
    if (!editor) return;
    if (loadedForLang.current === lang) return;
    loadedForLang.current = lang;
    editor.commands.setContent(toDocJson(paragraphs), { emitUpdate: false });
    // Only re-syncs on an explicit lang switch; the editor otherwise owns its own doc between saves.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor, lang]);

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
