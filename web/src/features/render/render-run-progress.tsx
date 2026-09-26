import type { PipelineStep } from "../../api/gen/types.gen";
import { InlineError } from "../../components/shared/inline-error";
import { Button } from "../../components/ui/button";
import { humanizeKind } from "../../lib/format";
import { useCancelRun, useRetryStep } from "../jobs/use-jobs";
import { runGroups } from "./render-model";

/**
 * Live counters of the active render run, one row per stage of the render
 * DAG, patched by SSE step events. Failed steps show their cause with a
 * retry; the whole run can be canceled.
 */
export function RenderRunProgress({ runId, steps }: { runId: string; steps: PipelineStep[] | undefined }) {
  const retry = useRetryStep();
  const cancel = useCancelRun();
  const groups = runGroups(steps ?? []);
  const failed = (steps ?? []).filter((s) => s.status === "failed");

  return (
    <section aria-label="Render progress" className="flex flex-col gap-3 rounded-md border border-border p-3">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">Rendering</h2>
        <Button variant="secondary" size="sm" disabled={cancel.isPending} onClick={() => cancel.mutate(runId)}>
          Cancel render
        </Button>
      </div>
      {!steps ? (
        <p className="text-sm text-text-2">Loading steps…</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {groups.map((g) => (
            <li key={g.key} className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 text-sm">
              <span>{g.label}</span>
              <span className="font-mono text-xs tabular-nums text-text-2">
                {g.done}/{g.total}
                {g.running > 0 && ` · ${g.running} running`}
                {g.failed > 0 && <span className="text-destructive"> · {g.failed} failed</span>}
              </span>
              <span className="col-span-2 h-1 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={g.label} aria-valuemin={0} aria-valuemax={g.total} aria-valuenow={g.done}>
                <span className={`block h-full ${g.failed > 0 ? "bg-destructive" : "bg-primary"}`} style={{ width: `${g.total ? (g.done / g.total) * 100 : 0}%` }} />
              </span>
            </li>
          ))}
        </ul>
      )}
      {failed.slice(0, 5).map((s) => (
        <InlineError key={s.id} cause={`${humanizeKind(s.kind)} failed: ${s.errorMsg ?? s.errorCode ?? "unknown error"}`} onRetry={() => retry.mutate(s.id)} />
      ))}
      {failed.length > 5 && <p className="text-xs text-text-2">{failed.length - 5} more failed steps are listed in the Render Queue.</p>}
    </section>
  );
}
