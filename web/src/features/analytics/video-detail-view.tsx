import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { useMemo } from "react";

import { InlineError } from "../../components/shared/inline-error";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { Button } from "../../components/ui/button";
import type { AnalyticsVideoDay, RetentionPoint } from "../../api/gen/types.gen";
import { AnalyticsChart, dayToUnix, type ChartSpec } from "./analytics-chart";
import { formatCount, formatRatioPercent, formatSeconds, metricCell } from "./analytics-format";
import { SuggestionsPanel } from "./suggestions-panel";
import { SyncStamp } from "./sync-stamp";
import { useAnalyticsVideo, useCanEditAnalytics, useUntrackVideo } from "./use-analytics";

export function videoDailySpec(days: AnalyticsVideoDay[]): ChartSpec {
  return {
    xKind: "time",
    x: days.map((d) => dayToUnix(d.date)),
    height: 200,
    series: [
      { label: "Views", values: days.map((d) => d.views ?? null), color: "#4db6a0", format: formatCount },
      { label: "CTR", values: days.map((d) => d.ctr ?? null), color: "#d9a441", format: formatRatioPercent, scale: "ctr" },
    ],
  };
}

export function retentionSpec(points: RetentionPoint[]): ChartSpec {
  const hasRelative = points.some((p) => p.relativeRetentionPerformance !== undefined);
  return {
    xKind: "ratio",
    x: points.map((p) => p.elapsedRatio),
    height: 200,
    series: [
      {
        label: "Audience watching",
        values: points.map((p) => p.audienceWatchRatio ?? null),
        color: "#4db6a0",
        format: (v) => `${Math.round(v * 100)}%`,
      },
      ...(hasRelative
        ? [
            {
              label: "Relative to similar videos",
              values: points.map((p) => p.relativeRetentionPerformance ?? null),
              color: "#a8a6a0",
              format: (v: number) => v.toFixed(2),
              scale: "relative",
              dash: [4, 4],
            },
          ]
        : []),
    ],
  };
}

/** Sums one metric over the window; undefined when no day has it. */
function total(days: AnalyticsVideoDay[], pick: (d: AnalyticsVideoDay) => number | undefined): number | undefined {
  const present = days.map(pick).filter((v): v is number => v !== undefined);
  return present.length === 0 ? undefined : present.reduce((a, b) => a + b, 0);
}

/** Impression-weighted CTR over the window (a plain average would over-weight quiet days). */
export function weightedCtr(days: AnalyticsVideoDay[]): number | undefined {
  let impressions = 0;
  let clicks = 0;
  for (const d of days) {
    if (d.impressions !== undefined && d.ctr !== undefined) {
      impressions += d.impressions;
      clicks += d.impressions * d.ctr;
    }
  }
  return impressions > 0 ? clicks / impressions : undefined;
}

/** Per-video page: daily views and CTR, the retention curve and this video's suggestions. */
export function VideoDetailView({ videoId, channelId }: { videoId: string; channelId?: string }) {
  const detail = useAnalyticsVideo(videoId);
  const untrack = useUntrackVideo();
  const canEdit = useCanEditAnalytics();
  const navigate = useNavigate();
  const data = detail.data;
  const dailySpec = useMemo(() => videoDailySpec(data?.days ?? []), [data?.days]);
  const curveSpec = useMemo(() => retentionSpec(data?.retention ?? []), [data?.retention]);

  if (detail.isError) {
    return <InlineError cause="Could not load this video. It may no longer be tracked." onRetry={() => void detail.refetch()} />;
  }
  if (!data) {
    return <div className="h-64 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />;
  }

  const ctx = { reachThrough: data.sync.reachThrough };
  const stats = [
    { label: "Views", cell: metricCell(total(data.days, (d) => d.views), "count", "views", ctx) },
    { label: "Watch time", cell: metricCell(total(data.days, (d) => d.watchHours), "hours", "estimatedMinutesWatched", ctx) },
    { label: "Impressions", cell: metricCell(total(data.days, (d) => d.impressions), "count", "impressions", ctx) },
    { label: "CTR", cell: metricCell(weightedCtr(data.days), "ratioPercent", "ctr", ctx) },
  ];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <Link to="/analytics" search={channelId ? { channel: channelId } : {}} className="flex items-center gap-1 text-xs text-text-2 hover:text-foreground">
            <ArrowLeft size={14} aria-hidden="true" />
            Analytics
          </Link>
          <h2 className="text-lg font-semibold">{data.video.title}</h2>
          <span className="text-xs text-text-2">
            {data.video.durationSeconds !== undefined && `${formatSeconds(data.video.durationSeconds)} · `}
            {data.video.source === "manual" ? "Tracked manually" : "Published by Loomtale"}
          </span>
        </div>
        <div className="flex gap-2">
          <Button asChild size="sm" variant="ghost">
            <a href={`https://www.youtube.com/watch?v=${encodeURIComponent(data.video.videoId)}`} target="_blank" rel="noreferrer noopener">
              <ExternalLink aria-hidden="true" />
              Open on YouTube
            </a>
          </Button>
          {canEdit && data.video.source === "manual" && (
            <Button
              size="sm"
              variant="destructive"
              disabled={untrack.isPending}
              onClick={() =>
                untrack.mutate(data.video.videoId, {
                  onSuccess: () => void navigate({ to: "/analytics", search: { channel: data.video.channelId } }),
                })
              }
            >
              Stop tracking
            </Button>
          )}
        </div>
      </div>
      <SyncStamp sync={data.sync} />
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        {stats.map((s) => (
          <div key={s.label} className="flex flex-col gap-0.5 rounded-md border border-border bg-card px-3 py-2" title={s.cell.reason}>
            <span className="text-2xs uppercase tracking-[0.04em] text-text-2">{s.label}</span>
            <span className={s.cell.missing ? "text-xs text-text-2" : "font-mono text-lg tabular-nums"}>{s.cell.text}</span>
          </div>
        ))}
      </div>
      <InspectorSection title="Daily views and CTR">
        {data.days.length > 0 ? (
          <AnalyticsChart spec={dailySpec} ariaLabel="Daily views and click-through rate" />
        ) : (
          <p className="text-sm text-text-2">No synced days in this window yet.</p>
        )}
      </InspectorSection>
      <InspectorSection title="Audience retention">
        {data.retention.length > 0 ? (
          <AnalyticsChart spec={curveSpec} ariaLabel="Share of the audience still watching at each point of the video" />
        ) : (
          <p className="text-sm text-text-2">No retention curve yet. YouTube reports it once the video has enough views.</p>
        )}
      </InspectorSection>
      <InspectorSection title="Suggestions">
        <SuggestionsPanel channelId={data.video.channelId} videoId={data.video.videoId} />
      </InspectorSection>
    </div>
  );
}
