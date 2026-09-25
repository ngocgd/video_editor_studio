import { useQuery } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";

import { getGpuStatusOptions, getReadyzOptions } from "../../api/gen/@tanstack/react-query.gen";
import { GpuStatusBar } from "./gpu-status-bar";

/**
 * Status bar (guidelines §6): 28px, always visible, GPU slot + queue +
 * VRAM + a backup-freshness warning (RT#10) sourced from /readyz's "backup"
 * detail. `role=status` + `aria-live=polite` for job completion messages.
 */
export function StatusBar() {
  const gpuQuery = useQuery({ ...getGpuStatusOptions(), refetchInterval: 4000 });
  const readyQuery = useQuery({ ...getReadyzOptions(), refetchInterval: 60_000, retry: false });

  const backupCheck = readyQuery.data?.checks?.backup;
  const backupStale = backupCheck != null && backupCheck !== "ok" && backupCheck !== "backup in progress";

  return (
    <footer
      role="status"
      aria-live="polite"
      className="flex h-7 shrink-0 items-center justify-between border-t border-border bg-background px-3"
    >
      <GpuStatusBar status={gpuQuery.data} />
      {backupStale && (
        <span className="flex items-center gap-1 text-xs text-warning">
          <TriangleAlert size={14} strokeWidth={1.75} aria-hidden="true" />
          Backup {backupCheck}
        </span>
      )}
    </footer>
  );
}
