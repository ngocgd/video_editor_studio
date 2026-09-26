import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { listRunStepsOptions } from "../../api/gen/@tanstack/react-query.gen";
import { ApiError } from "../../api/client";
import { useSseTopics } from "../../api/use-sse-topics";
import { JobProgress } from "../../components/shared/job-progress";
import { useGenerateSeries } from "./use-series";

/**
 * "New series from settings" wizard (phase 6): POST /series/{id}/generate,
 * then live progress over SSE via the shared JobProgress until every step
 * in the run is terminal, then hands off to the caller (routes into the
 * series' bible/episode list).
 */
export function SeriesGenerateWizard({ seriesId, onDone }: { seriesId: string; onDone: () => void }) {
  const generate = useGenerateSeries();
  const [runId, setRunId] = useState<string | null>(null);

  useEffect(() => {
    if (runId || generate.isPending) return;
    generate.mutate({ path: { id: seriesId }, body: {} }, { onSuccess: (res) => res && setRunId(res.runId) });
    // Fires once on mount; the mutation object itself is not a stable dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seriesId]);

  const { ready } = useSseTopics(runId ? [runId] : []);
  const stepsQuery = useQuery({
    ...listRunStepsOptions({ path: { id: runId ?? "" }, query: { limit: 50 } }),
    enabled: !!runId && ready,
    refetchInterval: 5000,
  });

  const steps = stepsQuery.data?.items ?? [];
  const allDone = runId != null && steps.length > 0 && steps.every((s) => s.status === "done");
  const anyFailed = steps.some((s) => s.status === "failed");

  useEffect(() => {
    if (allDone) onDone();
  }, [allDone, onDone]);

  if (generate.isError) {
    return <p className="text-sm text-destructive">{(generate.error as ApiError).message}</p>;
  }

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border bg-card p-4">
      <h2 className="text-md font-medium">Generating bible and episode outlines</h2>
      {steps.length === 0 && <p className="text-sm text-text-2">Starting…</p>}
      {steps.map((step) => (
        <JobProgress
          key={step.id}
          kind={step.kind}
          status={step.status}
          progress={step.progress}
          etaS={step.etaS}
          errorDetail={step.errorMsg}
        />
      ))}
      {anyFailed && <p className="text-xs text-destructive">One or more steps failed; retry from the render queue.</p>}
    </div>
  );
}
