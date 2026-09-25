import type { TargetLanguage } from "../../api/gen/types.gen";
import { Kbd } from "../../components/shared/kbd";
import { countWords, formatDurationEstimate } from "./duration-estimate";
import type { DiffParagraph } from "./paragraph-diff";

/** Duration footer (phase 6): live word count -> duration.Estimate-equivalent, "uncalibrated". */
export function WriterFooter({
  paragraphs,
  lang,
  saving,
  onToggleLang,
}: {
  paragraphs: DiffParagraph[];
  lang: TargetLanguage;
  saving: boolean;
  onToggleLang: () => void;
}) {
  const words = countWords(paragraphs);
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
      <span className={saving ? "text-info" : "text-success"}>{saving ? "Saving…" : "Saved"}</span>
    </footer>
  );
}
