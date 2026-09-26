import type { TargetLanguage } from "../../api/gen/types.gen";
import { Kbd } from "../../components/shared/kbd";
import type { SaveStatus } from "./draft-autosave-queue";
import { countWords, formatDurationEstimate } from "./duration-estimate";
import type { DiffParagraph } from "./paragraph-diff";

const SAVE_LABEL: Record<SaveStatus, { text: string; className: string }> = {
  saved: { text: "Saved", className: "text-success" },
  unsaved: { text: "Unsaved changes", className: "text-text-2" },
  saving: { text: "Saving…", className: "text-info" },
  retrying: { text: "Not saved, retrying…", className: "text-warning" },
  conflict: { text: "Not saved: changed elsewhere", className: "text-destructive" },
};

/** Duration footer (phase 6): live word count -> duration.Estimate-equivalent, "uncalibrated". */
export function WriterFooter({
  paragraphs,
  lang,
  saveStatus,
  onToggleLang,
}: {
  paragraphs: DiffParagraph[];
  lang: TargetLanguage;
  saveStatus: SaveStatus;
  onToggleLang: () => void;
}) {
  const words = countWords(paragraphs);
  const save = SAVE_LABEL[saveStatus];
  return (
    <footer className="flex h-8 shrink-0 items-center gap-4 border-t border-border bg-background px-3 text-xs text-text-2">
      <span className="font-mono tabular-nums">{words.toLocaleString()} words</span>
      <span className="font-mono tabular-nums">{formatDurationEstimate(words, lang)}</span>
      <button
        type="button"
        onClick={onToggleLang}
        className="ml-auto flex items-center gap-1.5 rounded-md px-2 py-0.5 hover:bg-accent hover:text-foreground"
      >
        {lang.toUpperCase()} draft <Kbd>Ctrl Alt L</Kbd>
      </button>
      <span role="status" className={save.className}>
        {save.text}
      </span>
    </footer>
  );
}
