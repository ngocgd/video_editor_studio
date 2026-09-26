import { RefreshCw } from "lucide-react";
import { useMemo } from "react";

import { InlineError } from "../../components/shared/inline-error";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { Button } from "../../components/ui/button";
import type { AnalyticsChannelDay, AnalyticsOverview } from "../../api/gen/types.gen";
import { AnalyticsChart, dayToUnix, type ChartSpec } from "./analytics-chart";
import { formatCount, formatDay, formatHours, metricCell } from "./analytics-format";
import { windowTotals } from "./analytics-selectors";
import { SyncStamp } from "./sync-stamp";
import { useAnalyticsOverview, useCanEditAnalytics, useSyncAnalytics } from "./use-analytics";
import { YppProgressBars } from "./ypp-progress";

export function overviewChartSpec(days: AnalyticsChannelDay[]): ChartSpec {
  return {
    xKind: "time",
    x: days.map((d) => dayToUnix(d.date)),
    height: 220,
    series: [
      { label: "Views", values: days.map((d) => d.views ?? null), color: "#4db6a0", format: formatCount },
      { label: "Watch hours", values: days.map((d) => d.watchHours ?? null), color: "#d9a441", format: formatHours, scale: "hours" },
    ],
  };
}

function Stat({ label, value, reason }: { label: string; value: string; reason?: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-md border border-border bg-card px-3 py-2" title={reason}>
      <span className="text-2xs uppercase tracking-[0.04em] text-text-2">{label}</span>
      <span className="font-mono text-lg tabular-nums text-foreground">{value}</span>
    </div>
  );
}

export function OverviewPanel({ overview, onSync, canSync, syncing }: { overview: AnalyticsOverview; onSync?: () => void; canSync: boolean; syncing: boolean }) {
  const totals = useMemo(() => windowTotals(overview.days), [overview.days]);
  const spec = useMemo(() => overviewChartSpec(overview.days), [overview.days]);
  const views = metricCell(totals.views, "count", "views");
  const hours = metricCell(totals.watchHours, "hours", "estimatedMinutesWatched");
  const subs = metricCell(totals.subscribersNet, "count", "subscribersGained");
  const hasData = overview.days.some((d) => d.views !== undefined || d.watchHours !== undefined);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <SyncStamp sync={overview.sync} />
        {canSync && (
          <Button size="sm" onClick={onSync} disabled={syncing || overview.sync.status === "running"}>
            <RefreshCw aria-hidden="true" />
            Sync now
          </Button>
        )}
      </div>
      <p className="text-xs text-text-2">
        {formatDay(overview.window.from)} to {formatDay(overview.window.to)}
      </p>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Views" value={views.text} reason={views.reason} />
        <Stat label="Watch time" value={hours.text} reason={hours.reason} />
        <Stat label="Net subscribers" value={subs.text} reason={subs.reason} />
        <Stat
          label="Subscribers"
          value={overview.sync.subscriberCount === undefined ? metricCell(undefined, "count", "subscriberCount").text : formatCount(overview.sync.subscriberCount)}
        />
      </div>
      <InspectorSection title="Daily views and watch hours">
        {hasData ? (
          <AnalyticsChart spec={spec} ariaLabel="Daily views and watch hours for the selected window" />
        ) : (
          <p className="text-sm text-text-2">No synced days in this window yet. Figures appear after the first sync.</p>
        )}
      </InspectorSection>
      <InspectorSection title="YouTube Partner Program">
        <YppProgressBars ypp={overview.ypp} />
      </InspectorSection>
    </div>
  );
}

/** Channel overview: data-through stamps, window totals, daily trend chart and YPP progress. */
export function ChannelOverview({ channelId }: { channelId: string }) {
  const overview = useAnalyticsOverview(channelId);
  const sync = useSyncAnalytics();
  const canEdit = useCanEditAnalytics();

  if (overview.isError) {
    return <InlineError cause="Could not load the channel overview." onRetry={() => void overview.refetch()} />;
  }
  if (!overview.data) {
    return <div className="h-64 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />;
  }
  return <OverviewPanel overview={overview.data} canSync={canEdit} syncing={sync.isPending} onSync={() => sync.mutate(channelId)} />;
}
