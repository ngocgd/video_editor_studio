import { Link } from "@tanstack/react-router";

import type { GpuStatus } from "../../api/gen/types.gen";
import { formatMb, formatPercent, humanizeKind } from "../../lib/format";

/**
 * `GPU [running job name] 61% · 3 queued · VRAM 13.8/16 GB` (guidelines §7).
 * The single source for GPU queue state in the status bar; clicking opens
 * the Render Queue (guidelines §6). The queue preview uses a native
 * `title` instead of a Radix tooltip so this status-bar-only component
 * does not pull Tooltip + floating-ui into every authenticated page's
 * first paint (review "bundle easy wins").
 */
export function GpuStatusBar({ status }: { status: GpuStatus | undefined }) {
  if (!status) {
    return <span className="text-xs text-muted-foreground">GPU status unavailable</span>;
  }

  const vramPct = status.vram ? (1 - status.vram.freeMb / status.vram.totalMb) * 100 : undefined;
  const vramWarning = vramPct != null && vramPct >= 90;
  const runningLabel = status.running ? (status.resident?.model ?? humanizeKind(status.running.kind)) : "Idle";
  const queuePreview = status.queue.length
    ? status.queue.map((step, index) => `#${index + 1} ${humanizeKind(step.kind)} (${formatPercent(step.progress)})`).join("\n")
    : "Queue is empty";

  return (
    <Link
      to="/jobs"
      title={queuePreview}
      className="flex items-center gap-1.5 text-xs text-text-2 hover:text-foreground"
    >
      <span className="font-medium text-foreground">GPU</span>
      <span className="max-w-40 truncate">{runningLabel}</span>
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
    </Link>
  );
}
