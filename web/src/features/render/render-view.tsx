import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Clapperboard, Info } from "lucide-react";
import { useEffect, useState } from "react";

import { getEpisodeOptions, getSeriesOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { SceneLanguage } from "../../api/gen/types.gen";
import { DiskChip } from "./disk-chip";
import { errorMessage, liveStages } from "./render-model";
import { RenderResults } from "./render-results";
import { RenderRunProgress } from "./render-run-progress";
import { RenderSettingsPanel } from "./render-settings-panel";
import { RenderStageStrip } from "./render-stage-strip";
import { useRenderRun, useRenders, useRenderStatus, useSaveRenderSettings, useStartRender } from "./use-render";

/**
 * The episode render page (wireframe render-queue): stage summary, the
 * "Render episode" button (disabled with the reasons while prerequisites
 * or disk space are missing), live progress of the active render, the
 * finished renders with their QC report, and the settings panel.
 */
export function RenderView({ seriesId, episodeId }: { seriesId: string; episodeId: string }) {
  const series = useQuery(getSeriesOptions({ path: { id: seriesId } }));
  const episode = useQuery(getEpisodeOptions({ path: { id: episodeId } }));
  const langs = series.data?.targetLanguages ?? ["en"];
  const [lang, setLang] = useState<SceneLanguage>("en");
  useEffect(() => {
    const first = series.data?.targetLanguages[0];
    if (first) setLang(first);
  }, [series.data?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const status = useRenderStatus(episodeId, lang);
  const renders = useRenders(episodeId, lang);
  const save = useSaveRenderSettings(episodeId, lang);
  const start = useStartRender(episodeId, lang);
  const activeRunId = status.data?.activeRunId;
  const run = useRenderRun(episodeId, lang, activeRunId);

  const data = status.data;
  const stages = data ? liveStages(data.stages, run.steps) : [];
  const latest = data?.latest;
  const epLabel = `Ep ${String(episode.data?.idx ?? 0).padStart(2, "0")}`;
  const running = Boolean(activeRunId);

  return (
    <div className="-m-4 grid h-[calc(100%+2rem)] min-h-0 grid-cols-[minmax(0,1fr)_360px]">
      <section aria-label="Render" className="flex min-h-0 flex-col gap-3 overflow-auto p-4">
        <header className="flex flex-wrap items-center gap-2">
          <div className="flex flex-col">
            <span className="text-xs text-text-2">
              <Link to="/projects/$seriesId" params={{ seriesId }} className="hover:underline">
                {series.data?.title ?? "Series"}
              </Link>{" "}
              · {epLabel}
            </span>
            <h1 className="text-lg font-semibold">{episode.data?.title ?? "Render"}</h1>
          </div>
          <nav aria-label="Episode" className="ml-4 flex gap-1 text-sm">
            <Link to="/projects/$seriesId/episodes/$episodeId" params={{ seriesId, episodeId }} className="rounded-md px-2 py-1 text-text-2 hover:bg-accent">
              Draft
            </Link>
            <Link to="/projects/$seriesId/storyboard/$episodeId" params={{ seriesId, episodeId }} className="rounded-md px-2 py-1 text-text-2 hover:bg-accent">
              Storyboard
            </Link>
            <span aria-current="page" className="rounded-md bg-accent px-2 py-1">
              Render
            </span>
          </nav>
          <span className="flex-1" />
          {data && <DiskChip disk={data.disk} />}
          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground disabled:opacity-50"
            disabled={!data?.ready || start.isPending}
            aria-describedby={data && !data.ready ? "render-blocked-reasons" : undefined}
            onClick={() => start.mutate({ path: { id: episodeId }, body: { lang } })}
          >
            <Clapperboard size={14} aria-hidden="true" /> {running ? "Rendering…" : "Render episode"}
          </button>
        </header>

        {start.error && (
          <p role="alert" className="text-sm text-destructive">
            {errorMessage(start.error)}
          </p>
        )}
        {status.error && (
          <p role="alert" className="text-sm text-destructive">
            {errorMessage(status.error)}
          </p>
        )}

        {data && <RenderStageStrip stages={stages} encoder={data.estimate?.encoder ?? data.settings.encoder} />}

        {latest?.restartedAfterEdit && (
          <p role="status" className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
            <Info size={14} className="text-info" aria-hidden="true" />
            Render restarted after an edit at {new Date(latest.createdAt).toLocaleTimeString()}; {latest.reusedSegments} unchanged segments were reused.
          </p>
        )}

        {data && !data.ready && data.reasons.length > 0 && (
          <div id="render-blocked-reasons" className="rounded-md border border-border p-3">
            <h2 className="mb-1 text-sm font-medium">Why the render cannot start yet</h2>
            <ul className="list-disc pl-5 text-sm text-text-2">
              {data.reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        )}

        {activeRunId && <RenderRunProgress runId={activeRunId} steps={run.steps} />}

        <h2 className="text-sm font-medium">Renders</h2>
        {renders.isLoading ? <p className="text-sm text-text-2">Loading renders…</p> : <RenderResults renders={renders.data?.items ?? []} />}
      </section>
      <RenderSettingsPanel
        episodeTitle={epLabel}
        settings={data?.settings}
        langs={langs}
        lang={lang}
        onLangChange={setLang}
        estimate={data?.estimate}
        saving={save.isPending}
        saveError={errorMessage(save.error)}
        onSave={(body) => save.mutate({ path: { id: episodeId, lang }, body })}
      />
    </div>
  );
}
