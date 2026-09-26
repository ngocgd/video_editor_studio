import { useState } from "react";

import type { TargetLanguage } from "../../api/gen/types.gen";
import { useSeriesList } from "../series/use-series";
import { ImportRightsBanner } from "./import-rights-banner";
import { ImportPreview } from "./import-preview";
import { useCreateImport } from "./use-import";
import { useImportUpload } from "./use-import-upload";

/** Chapter import flow (phase 6): upload -> preview (split preset) -> commit to episodes. */
export function ImportView() {
  const seriesQuery = useSeriesList();
  const upload = useImportUpload();
  const createImport = useCreateImport();

  const [seriesId, setSeriesId] = useState("");
  const [importId, setImportId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [translateToLang, setTranslateToLang] = useState<TargetLanguage | "">("");

  const handleFile = async (file: File) => {
    setError(null);
    if (!seriesId) {
      setError("Pick a series first.");
      return;
    }
    try {
      const assetId = await upload.upload(file);
      const imported = await createImport.mutateAsync({ body: { assetId, seriesId } });
      if (imported) setImportId(imported.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed.");
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold">Import chapters</h1>
      <ImportRightsBanner />

      {!importId && (
        <div className="flex max-w-md flex-col gap-3 rounded-md border border-border bg-card p-4">
          <label className="flex flex-col gap-1.5 text-sm">
            Series
            <select
              className="h-8 rounded-md border border-input bg-well px-2 text-sm"
              value={seriesId}
              onChange={(e) => setSeriesId(e.target.value)}
            >
              <option value="">Select a series…</option>
              {(seriesQuery.data?.items ?? []).map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1.5 text-sm">
            Manuscript (.txt or .md, up to 10MB)
            <input
              type="file"
              accept=".txt,.md,text/plain,text/markdown"
              disabled={upload.isUploading}
              onChange={(e) => e.target.files?.[0] && void handleFile(e.target.files[0])}
            />
          </label>

          {upload.progressLabel && <p className="text-xs text-text-2">{upload.progressLabel}</p>}
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
      )}

      {importId && (
        <ImportPreview importId={importId} translateToLang={translateToLang} onTranslateToLangChange={setTranslateToLang} />
      )}
    </div>
  );
}
