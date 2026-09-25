import type { ChapterPreview } from "../../api/gen/types.gen";

/** Presentational chapter list from an import preview (phase 6): index, title, word count, selectable for commit. */
export function ChapterPreviewTable({
  chapters,
  selected,
  onToggle,
}: {
  chapters: ChapterPreview[];
  /** `null` means "every chapter selected" (the API default when chapterIndexes is empty). */
  selected: Set<number> | null;
  onToggle: (index: number) => void;
}) {
  if (chapters.length === 0) {
    return <p className="text-sm text-text-2">No chapters matched this split preset.</p>;
  }

  return (
    <table className="w-full border-collapse text-sm">
      <thead>
        <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
          <th className="h-7 px-2" />
          <th className="h-7 px-2">#</th>
          <th className="h-7 px-2">Title</th>
          <th className="h-7 px-2">Words</th>
        </tr>
      </thead>
      <tbody>
        {chapters.map((chapter) => (
          <tr key={chapter.index} className="border-b border-border">
            <td className="p-1.5">
              <input
                type="checkbox"
                aria-label={`Include chapter ${chapter.index}`}
                checked={selected ? selected.has(chapter.index) : true}
                onChange={() => onToggle(chapter.index)}
              />
            </td>
            <td className="p-1.5 font-mono text-text-2">{chapter.index}</td>
            <td className="p-1.5">{chapter.title}</td>
            <td className="p-1.5 font-mono text-text-2">{chapter.wordCount}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
