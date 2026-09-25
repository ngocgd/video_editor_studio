import { useState } from "react";

import type { ImportPreviewRequest, TargetLanguage } from "../../api/gen/types.gen";
import { ApiError } from "../../api/client";
import { Button } from "../../components/ui/button";
import { ChapterPreviewTable } from "./chapter-preview-table";
import { useCommitImport, useImport, usePreviewImport } from "./use-import";

const SPLIT_PRESETS: { value: NonNullable<ImportPreviewRequest["splitPreset"]>; label: string }[] = [
  { value: "auto", label: "Auto-detect" },
  { value: "zh_chapter", label: "第N章 (Chinese)" },
  { value: "en_chapter", label: "Chapter N (English)" },
  { value: "vi_chuong", label: "Chương N (Vietnamese)" },
];

/** Split-preset selector + resulting chapter list + commit (phase 6 import preview/commit). */
export function ImportPreview({
  importId,
  translateToLang,
  onTranslateToLangChange,
}: {
  importId: string;
  translateToLang: TargetLanguage | "";
  onTranslateToLangChange: (lang: TargetLanguage | "") => void;
}) {
  const importQuery = useImport(importId);
  const preview = usePreviewImport(importId);
  const commit = useCommitImport();
  const [preset, setPreset] = useState<ImportPreviewRequest["splitPreset"]>("auto");
  const [selected, setSelected] = useState<Set<number> | null>(null);
  const [committed, setCommitted] = useState<string[] | null>(null);

  const imp = importQuery.data;
  const chapters = imp?.chapters ?? [];

  const runPreview = () => {
    setSelected(null);
    preview.mutate({ path: { id: importId }, body: { splitPreset: preset } });
  };

  const toggleChapter = (index: number) => {
    setSelected((current) => {
      const next = new Set(current ?? chapters.map((c) => c.index));
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };

  const doCommit = async () => {
    if (!imp?.seriesId) return;
    const chapterIndexes = selected ? [...selected] : [];
    const res = await commit.mutateAsync({
      path: { id: importId },
      body: { seriesId: imp.seriesId, chapterIndexes, translateToLang: translateToLang || undefined },
    });
    if (res) setCommitted(res.episodeIds);
  };

  if (committed) {
    return <p className="text-sm text-success">Committed {committed.length} episode(s) from this import.</p>;
  }

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border bg-card p-4">
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-sm">
          Split preset
          <select className="h-8 rounded-md border border-input bg-well px-2 text-sm" value={preset} onChange={(e) => setPreset(e.target.value as typeof preset)}>
            {SPLIT_PRESETS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        <Button variant="secondary" size="sm" onClick={runPreview} disabled={preview.isPending}>
          {preview.isPending ? "Splitting…" : "Preview split"}
        </Button>
      </div>

      {preview.isError && <p className="text-xs text-destructive">{(preview.error as ApiError).message}</p>}

      {chapters.length > 0 && (
        <>
          <ChapterPreviewTable chapters={chapters} selected={selected} onToggle={toggleChapter} />

          <label className="flex items-center gap-1.5 text-sm">
            Translate to
            <select
              className="h-8 rounded-md border border-input bg-well px-2 text-sm"
              value={translateToLang}
              onChange={(e) => onTranslateToLangChange(e.target.value as TargetLanguage | "")}
            >
              <option value="">No translation</option>
              <option value="en">English</option>
              <option value="vi">Vietnamese</option>
            </select>
          </label>

          <Button variant="primary" size="sm" className="w-fit" onClick={doCommit} disabled={commit.isPending}>
            {commit.isPending ? "Committing…" : "Commit selected chapters"}
          </Button>
        </>
      )}
    </div>
  );
}
