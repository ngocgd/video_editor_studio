import type { CleanupPreview, LibrarySeriesUsage } from "../../api/gen/types.gen";
import { formatBytes } from "../../lib/format";

const REFERENCE_LABELS: Record<string, string> = {
  selected_take: "Selected take",
  take: "Unselected take",
  segment: "Render cache",
  render: "Render",
  character: "Character reference",
};

/** "Selected take, Render" or "Unused" for an asset nothing references. */
export function referenceLabel(refs: string[]): string {
  if (refs.length === 0) return "Unused";
  return refs.map((r) => REFERENCE_LABELS[r] ?? r).join(", ");
}

/** Share of the total per project, largest first, for the usage bars. */
export function usageShares(series: LibrarySeriesUsage[], totalBytes: number): (LibrarySeriesUsage & { share: number; label: string })[] {
  return [...series]
    .sort((a, b) => b.bytes - a.bytes)
    .map((s) => ({ ...s, share: totalBytes > 0 ? s.bytes / totalBytes : 0, label: s.seriesTitle ?? (s.seriesId ? "Untitled project" : "Not in a project") }));
}

/** One sentence summing up a dry-run preview. */
export function previewSummary(p: CleanupPreview): string {
  const parts = [`${p.segments.length} expired render cache ${p.segments.length === 1 ? "entry" : "entries"}`, `${p.takes.length} unselected ${p.takes.length === 1 ? "take" : "takes"}`];
  const more = p.truncated ? " (the first batch; run cleanup again for the rest)" : "";
  return `${parts.join(" and ")}, ${formatBytes(p.bytes)} in total${more}.`;
}
