import { Button } from "../ui/button";
import { formatEta } from "../../lib/format";

export interface JobProgressProps {
  name: string;
  step: string;
  status: "pending" | "queued" | "running" | "done" | "failed" | "canceled";
  progress: number;
  etaS?: number;
  gpuQueuePosition?: number;
  onRetry?: () => void;
  onViewLog?: () => void;
  onCancel?: () => void;
}

/**
 * Job progress row (guidelines §7): name, step, determinate 4px bar, mono
 * ETA, cancel. Indeterminate while waiting on the GPU slot. Failed jobs
 * freeze the bar at the failure point in `--destructive` with recovery
 * actions.
 */
export function JobProgress({
  name,
  step,
  status,
  progress,
  etaS,
  gpuQueuePosition,
  onRetry,
  onViewLog,
  onCancel,
}: JobProgressProps) {
  const waitingForGpu = status === "queued" && gpuQueuePosition != null;
  const failed = status === "failed";
  const eta = formatEta(etaS);

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="flex min-w-0 flex-col">
          <span className="truncate font-medium text-foreground">{name}</span>
          <span className="truncate text-xs text-text-2">
            {waitingForGpu ? `Waiting for GPU slot (#${gpuQueuePosition})` : step}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {eta && !failed && <span className="font-mono text-xs text-text-2 tabular-nums">{eta}</span>}
          {status === "running" && onCancel && (
            <Button variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
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
        aria-label={`${name} progress`}
        className="h-1 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className="h-full rounded-full transition-[width] duration-[180ms] ease-out"
          style={{
            width: `${Math.max(0, Math.min(100, progress))}%`,
            backgroundColor: failed ? "var(--destructive)" : "var(--info)",
          }}
        />
      </div>
    </div>
  );
}
