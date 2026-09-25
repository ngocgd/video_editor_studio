import { Node, mergeAttributes } from "@tiptap/core";

/**
 * Replaces StarterKit's default paragraph node (disabled via
 * `StarterKit.configure({ paragraph: false })`) with one that carries an
 * `id` attribute matching the API's `DraftParagraph.id` (phase 6: "a
 * ProseMirror paragraph node must carry the paragraph's id attribute").
 * New paragraphs (Enter, paste) get a fresh client-generated id; the
 * autosave diff (see ./paragraph-diff) treats any id not in the last-saved
 * draft as new and emits `upsert` + `move` for it.
 */
export const ParagraphWithId = Node.create({
  name: "paragraph",
  priority: 1000,
  group: "block",
  content: "inline*",

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (element) => element.getAttribute("data-paragraph-id"),
        renderHTML: (attributes) => (attributes.id ? { "data-paragraph-id": attributes.id } : {}),
      },
    };
  },

  parseHTML() {
    return [{ tag: "p" }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["p", mergeAttributes(HTMLAttributes), 0];
  },

  addKeyboardShortcuts() {
    return {
      Enter: () =>
        this.editor.commands.first(({ commands }) => [
          () => commands.splitBlock(),
          () =>
            commands.updateAttributes(this.name, {
              id: `p_${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`,
            }),
        ]),
    };
  },
});

export function newParagraphId(): string {
  return `p_${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`;
}
