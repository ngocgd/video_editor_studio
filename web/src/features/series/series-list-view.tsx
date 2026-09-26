import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { EmptyState } from "../../components/shared/empty-state";
import { Button } from "../../components/ui/button";
import { SeriesCreateForm } from "./series-create-form";
import { SeriesGenerateWizard } from "./series-generate-wizard";
import { useSeriesList } from "./use-series";

/** Series list + create form + "new series from settings" generation wizard (phase 6). */
export function SeriesListView() {
  const { data, isLoading } = useSeriesList();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const [generatingSeriesId, setGeneratingSeriesId] = useState<string | null>(null);

  const series = data?.items ?? [];

  if (generatingSeriesId) {
    return (
      <SeriesGenerateWizard
        seriesId={generatingSeriesId}
        onDone={() => void navigate({ to: "/projects/$seriesId", params: { seriesId: generatingSeriesId } })}
      />
    );
  }

  if (creating) {
    return <SeriesCreateForm onCreated={(id) => setGeneratingSeriesId(id)} />;
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Projects</h1>
        <Button variant="primary" size="md" onClick={() => setCreating(true)}>
          New series
        </Button>
      </div>

      {isLoading && <p className="text-sm text-text-2">Loading…</p>}

      {!isLoading && series.length === 0 && (
        <EmptyState message="No series yet. Create your first one to start writing." actionLabel="New series" onAction={() => setCreating(true)} />
      )}

      {series.length > 0 && (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
              <th className="h-8 px-3">Title</th>
              <th className="h-8 px-3">Genre</th>
              <th className="h-8 px-3">Languages</th>
              <th className="h-8 px-3">Episodes</th>
              <th className="h-8 px-3">Status</th>
            </tr>
          </thead>
          <tbody>
            {series.map((s) => (
              <tr
                key={s.id}
                className="cursor-pointer border-b border-border hover:bg-card"
                onClick={() => void navigate({ to: "/projects/$seriesId", params: { seriesId: s.id } })}
              >
                <td className="h-9 px-3 font-medium">{s.title}</td>
                <td className="h-9 px-3 text-text-2">{s.genre ?? "—"}</td>
                <td className="h-9 px-3 text-text-2">{s.targetLanguages.join(", ").toUpperCase()}</td>
                <td className="h-9 px-3 font-mono text-text-2">{s.plannedEpisodeCount}</td>
                <td className="h-9 px-3 text-text-2">{s.status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
