import { useState } from "react";

import { formatEta, humanizeKind } from "../../lib/format";
import { Button } from "../ui/button";

export interface JobProgressProps {
  /** Raw backend step kind, e.g. "render_episode"; humanized for the label, kept in a mono detail for debugging. */
  kind: string;
  status: "pending" | "queued" | "running" | "done" | "failed" | "canceled";
  progress: number;
  etaS?: number;
  gpuQueuePosition?: number;
  errorDetail?: string;
  onRetry?: () => void;
  onViewLog?: () => void;
  onCancel?: () => void;
}

/**
 * Job progress row (guidelines §7): name, step, determinate 4px bar in the
 * jade accent, mono ETA, cancel with a confirm step. Indeterminate while
 * waiting on the GPU slot. Failed jobs freeze the bar at the failure point
 * in `--destructive` with recovery actions.
 */
export function JobProgress({
  kind,
  status,
  progress,
  etaS,
  gpuQueuePosition,
  errorDetail,
  onRetry,
  onViewLog,
  onCancel,
}: JobProgressProps) {
  const [confirmingCancel, setConfirmingCancel] = useState(false);
  const waitingForGpu = status === "queued" && gpuQueuePosition != null;
  const failed = status === "failed";
  const eta = formatEta(etaS);
  const label = humanizeKind(kind);
  const stepDetail = waitingForGpu ? `Waiting for GPU slot (#${gpuQueuePosition})` : failed ? errorDetail ?? "Step failed" : status;

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="flex min-w-0 flex-col">
          <span className="truncate font-medium text-foreground" title={kind}>
            {label}
          </span>
          <span className="truncate text-xs text-text-2">{stepDetail}</span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {eta && !failed && <span className="font-mono text-xs tabular-nums text-text-2">{eta}</span>}
          {status === "running" && onCancel && !confirmingCancel && (
            <Button variant="ghost" size="sm" onClick={() => setConfirmingCancel(true)}>
              Cancel
            </Button>
          )}
          {confirmingCancel && (
            <span className="flex items-center gap-1 text-xs text-text-2">
              Cancel run?
              <Button variant="destructive" size="sm" onClick={onCancel}>
                Yes
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirmingCancel(false)}>
                No
              </Button>
            </span>
          )}
          {failed && onRetry && (
            <Button variant="secondary" size="sm" onClick={onRetry}>
              Retry step
            </Button>
          )}
          {failed && onViewLog && (
            <Button variant="ghost" size="sm" onClick={onViewLog}>
              View log
            </Button>
          )}
        </div>
      </div>
      <div
        role="progressbar"
        aria-valuenow={Math.round(progress)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${label} progress`}
        className="h-1 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className="h-full rounded-full transition-[width] duration-[180ms] ease-out"
          style={{
            width: `${Math.max(0, Math.min(100, progress))}%`,
            backgroundColor: failed ? "var(--destructive)" : "var(--primary)",
          }}
        />
      </div>
    </div>
  );
}
