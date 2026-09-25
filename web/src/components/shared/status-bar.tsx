import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";

import { getReadyzOptions } from "../../api/gen/@tanstack/react-query.gen";
import { getSseBridge, type StreamStatus } from "../../api/sse-bridge";
import { useJobCompletionAnnouncement } from "../../api/use-job-completion-announcement";
import { useGpuStatus } from "../../features/jobs/use-jobs";
import { GpuStatusBar } from "./gpu-status-bar";

function useStreamStatus(): StreamStatus {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<StreamStatus>(() => getSseBridge(queryClient).getStatus());
  useEffect(() => getSseBridge(queryClient).onStatusChange(setStatus), [queryClient]);
  return status;
}

/**
 * Status bar (guidelines §6): 28px, always visible, GPU slot + queue +
 * VRAM + a backup-freshness warning (RT#10) sourced from /readyz's "backup"
 * detail. The GPU line changes every 4s, so it is deliberately outside the
 * `aria-live` region; only the dedicated completion-message span announces
 * (guidelines: "footer[role=status][aria-live=polite] for job completion").
 */
export function StatusBar() {
  const gpuQuery = useGpuStatus();
  const readyQuery = useQuery({ ...getReadyzOptions(), refetchInterval: 60_000, retry: false });
  const completionMessage = useJobCompletionAnnouncement();
  const streamStatus = useStreamStatus();

  const backupCheck = readyQuery.data?.checks?.backup;
  const backupStale = backupCheck != null && backupCheck !== "ok" && backupCheck !== "backup in progress";
  const backupLabel = backupCheck === "no backup run yet" ? "not run yet" : backupCheck;

  return (
    <footer className="flex h-7 shrink-0 items-center justify-between border-t border-border bg-background px-3">
      <div className="flex items-center gap-3">
        <GpuStatusBar status={gpuQuery.data} />
        {streamStatus === "degraded" && (
          <span className="text-xs text-muted-foreground" title="Live updates are paused; the page still refreshes periodically.">
            Live updates paused
          </span>
        )}
      </div>
      {backupStale && (
        <span
          className="flex items-center gap-1 text-xs text-warning"
          title="The nightly backup has not completed recently. See the restore drill runbook."
        >
          <TriangleAlert size={14} strokeWidth={1.75} aria-hidden="true" />
          Backup: {backupLabel}
        </span>
      )}
      <span role="status" aria-live="polite" className="sr-only">
        {completionMessage}
      </span>
    </footer>
  );
}
