import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { getSeriesOptions, listEpisodesOptions } from "../../api/gen/@tanstack/react-query.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { formatDurationEstimate } from "../writer/duration-estimate";

/** Series settings + episode list, with links into the bible editor and the writer (phase 6 sitemap: Projects -> Episodes). */
export function SeriesDetailView({ seriesId }: { seriesId: string }) {
  const seriesQuery = useQuery(getSeriesOptions({ path: { id: seriesId } }));
  const episodesQuery = useQuery(listEpisodesOptions({ query: { seriesId, limit: 200 } }));

  const series = seriesQuery.data;
  const episodes = episodesQuery.data?.items ?? [];

  if (!series) return <p className="text-sm text-text-2">Loading…</p>;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">{series.title}</h1>
          <p className="text-xs text-text-2">
            {series.genre ?? "No genre"} · {series.targetLanguages.join(", ").toUpperCase()} · {series.plannedEpisodeCount} episodes planned
          </p>
        </div>
        <div className="flex gap-2">
          <Link
            to="/projects/$seriesId/characters"
            params={{ seriesId }}
            className="rounded-md border border-border bg-secondary px-3 py-1.5 text-sm hover:bg-accent"
          >
            Characters
          </Link>
          <Link
            to="/projects/$seriesId/bible"
            params={{ seriesId }}
            className="rounded-md border border-border bg-secondary px-3 py-1.5 text-sm hover:bg-accent"
          >
            Story bible
          </Link>
        </div>
      </div>

      {episodes.length === 0 ? (
        <EmptyState message="No episodes yet. Generate outlines from series settings or import chapters." />
      ) : (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
              <th className="h-8 px-3">#</th>
              <th className="h-8 px-3">Title</th>
              <th className="h-8 px-3">Status</th>
              <th className="h-8 px-3">EN</th>
              <th className="h-8 px-3">VI</th>
              <th className="h-8 px-3" />
            </tr>
          </thead>
          <tbody>
            {episodes.map((ep) => (
              <tr key={ep.id} className="border-b border-border hover:bg-card">
                <td className="h-9 px-3 font-mono text-text-2">{ep.idx}</td>
                <td className="h-9 px-3">
                  <Link to="/projects/$seriesId/episodes/$episodeId" params={{ seriesId, episodeId: ep.id }} className="font-medium hover:underline">
                    {ep.title || "Untitled"}
                  </Link>
                </td>
                <td className="h-9 px-3 text-text-2">{ep.status}</td>
                <td className="h-9 px-3 text-text-2">
                  {ep.drafts?.en ? `${ep.drafts.en.wordCount ?? 0}w · ${formatDurationEstimate(ep.drafts.en.wordCount ?? 0, "en")}` : "—"}
                </td>
                <td className="h-9 px-3 text-text-2">
                  {ep.drafts?.vi ? `${ep.drafts.vi.wordCount ?? 0}w · ${formatDurationEstimate(ep.drafts.vi.wordCount ?? 0, "vi")}` : "—"}
                </td>
                <td className="h-9 px-3 text-right">
                  <Link to="/projects/$seriesId/storyboard/$episodeId" params={{ seriesId, episodeId: ep.id }} className="text-xs text-primary-text hover:underline">
                    Storyboard
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
