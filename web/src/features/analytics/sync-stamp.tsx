import type { AnalyticsSyncState } from "../../api/gen/types.gen";
import { formatDay } from "./analytics-format";

/**
 * The "data through <date>" stamps: the Analytics API and the reach report
 * lag by different amounts, so each source shows its own date.
 */
export function SyncStamp({ sync }: { sync: AnalyticsSyncState }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-text-2">
      <span>
        Analytics data through{" "}
        <span className="font-mono text-foreground">{sync.analyticsThrough ? formatDay(sync.analyticsThrough) : "not synced yet"}</span>
      </span>
      <span>
        Reach (impressions, CTR) through{" "}
        <span className="font-mono text-foreground">{sync.reachThrough ? formatDay(sync.reachThrough) : "pending first report"}</span>
      </span>
      {sync.status === "running" && <span className="text-primary-text">Sync running</span>}
      {sync.status === "failed" && <span className="text-destructive">Last sync failed</span>}
      {sync.lastError && (
        <span className="w-full text-warning" title={sync.lastError}>
          {sync.lastError}
        </span>
      )}
    </div>
  );
}
