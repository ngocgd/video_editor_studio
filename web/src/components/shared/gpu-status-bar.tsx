import * as TooltipPrimitive from "@radix-ui/react-tooltip";

import type { GpuStatus } from "../../api/gen/types.gen";
import { formatMb, formatPercent } from "../../lib/format";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/**
 * `GPU [running job name] 61% · 3 queued · VRAM 13.8/16 GB` (guidelines §7).
 * The single source for GPU queue state in the status bar; hover shows the
 * ordered queue.
 */
export function GpuStatusBar({ status }: { status: GpuStatus | undefined }) {
  if (!status) {
    return <span className="text-xs text-muted-foreground">GPU status unavailable</span>;
  }

  const vramPct = status.vram ? (1 - status.vram.freeMb / status.vram.totalMb) * 100 : undefined;
  const vramWarning = vramPct != null && vramPct >= 90;
  const runningLabel = status.running ? `${status.resident?.model ?? status.running.kind}` : "idle";

  return (
    <TooltipPrimitive.Provider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="flex items-center gap-1.5 text-xs text-text-2 hover:text-foreground"
          >
            <span className="font-medium text-foreground">GPU</span>
            <span className="truncate max-w-40">{runningLabel}</span>
            {status.running && <span className="font-mono tabular-nums">{formatPercent(status.running.progress)}</span>}
            <span aria-hidden="true">·</span>
            <span>{status.queue.length} queued</span>
            {status.vram && (
              <>
                <span aria-hidden="true">·</span>
                <span className={vramWarning ? "text-warning" : undefined}>
                  VRAM {formatMb(status.vram.totalMb - status.vram.freeMb)}/{formatMb(status.vram.totalMb)}
                </span>
              </>
            )}
            {status.workerOnline === false && <span className="text-destructive">· worker offline</span>}
          </button>
        </TooltipTrigger>
        <TooltipContent>
          <ul className="flex flex-col gap-1">
            {status.queue.length === 0 && <li className="text-muted-foreground">Queue is empty</li>}
            {status.queue.map((step, index) => (
              <li key={step.id}>
                #{index + 1} {step.kind} ({formatPercent(step.progress)})
              </li>
            ))}
          </ul>
        </TooltipContent>
      </Tooltip>
    </TooltipPrimitive.Provider>
  );
}
