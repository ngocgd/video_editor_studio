import { useNavigate } from "@tanstack/react-router";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useMemo, useState } from "react";

import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { VirtualTable, type VirtualTableColumn } from "../../components/shared/virtual-table";
import { Button } from "../../components/ui/button";
import type { AnalyticsVideoRow } from "../../api/gen/types.gen";
import { formatSeconds, metricCell, type MetricCell } from "./analytics-format";
import { useAnalyticsVideos, type SortOrder, type VideoSort } from "./use-analytics";

const SORT_OPTIONS: { value: VideoSort; label: string }[] = [
  { value: "views", label: "Views" },
  { value: "watchTime", label: "Watch time" },
  { value: "ctr", label: "CTR" },
  { value: "averageViewPercentage", label: "Avg. view %" },
  { value: "published", label: "Published" },
];

function Metric({ cell }: { cell: MetricCell }) {
  return (
    <span className={cell.missing ? "text-xs text-text-2" : "font-mono tabular-nums"} title={cell.reason}>
      {cell.text}
    </span>
  );
}

export function videoColumns(reachThrough: string | undefined): VirtualTableColumn<AnalyticsVideoRow>[] {
  const ctx = { reachThrough };
  const num = "w-28 shrink-0 truncate px-3 text-right";
  return [
    {
      key: "title",
      header: "Video",
      className: "min-w-0 flex-1 truncate px-3",
      render: (row) => (
        <span title={row.title}>
          {row.title}
          {row.durationSeconds !== undefined && <span className="ml-2 font-mono text-xs text-text-2">{formatSeconds(row.durationSeconds)}</span>}
        </span>
      ),
    },
    { key: "views", header: "Views", className: num, render: (row) => <Metric cell={metricCell(row.views, "count", "views", ctx)} /> },
    {
      key: "watch",
      header: "Watch time",
      className: num,
      render: (row) => <Metric cell={metricCell(row.watchHours, "hours", "estimatedMinutesWatched", ctx)} />,
    },
    {
      key: "avp",
      header: "Avg. view %",
      className: num,
      render: (row) => <Metric cell={metricCell(row.averageViewPercentage, "percent", "averageViewPercentage", ctx)} />,
    },
    { key: "impressions", header: "Impressions", className: num, render: (row) => <Metric cell={metricCell(row.impressions, "count", "impressions", ctx)} /> },
    { key: "ctr", header: "CTR", className: num, render: (row) => <Metric cell={metricCell(row.ctr, "ratioPercent", "ctr", ctx)} /> },
  ];
}

/** Virtualized, server-sorted table of the channel's tracked videos with cursor paging. */
export function VideoTable({ channelId }: { channelId: string }) {
  const [sort, setSort] = useState<VideoSort>("views");
  const [order, setOrder] = useState<SortOrder>("desc");
  const [activeIndex, setActiveIndex] = useState(0);
  const navigate = useNavigate();
  const videos = useAnalyticsVideos(channelId, sort, order);

  const rows = useMemo(() => videos.data?.pages.flatMap((page) => page.items) ?? [], [videos.data]);
  const first = videos.data?.pages[0];
  const columns = useMemo(() => videoColumns(first?.sync.reachThrough), [first?.sync.reachThrough]);

  function openVideo(row: AnalyticsVideoRow) {
    void navigate({ to: "/analytics/videos/$videoId", params: { videoId: row.videoId }, search: { channel: channelId } });
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <label htmlFor="analytics-sort" className="text-text-2">
          Sort by
        </label>
        <select
          id="analytics-sort"
          value={sort}
          onChange={(event) => {
            setSort(event.target.value as VideoSort);
            setActiveIndex(0);
          }}
          className="h-7 rounded-md border border-border bg-background px-2 text-sm"
        >
          {SORT_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <Button
          size="sm"
          variant="ghost"
          aria-label={order === "desc" ? "Sorted descending, switch to ascending" : "Sorted ascending, switch to descending"}
          onClick={() => setOrder(order === "desc" ? "asc" : "desc")}
        >
          {order === "desc" ? <ArrowDown aria-hidden="true" /> : <ArrowUp aria-hidden="true" />}
        </Button>
        {first && <span className="ml-auto text-xs text-text-2">{first.total} tracked videos</span>}
      </div>

      {videos.isError ? (
        <InlineError cause="Could not load the video table." onRetry={() => void videos.refetch()} />
      ) : videos.isLoading ? (
        <div className="h-40 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />
      ) : rows.length === 0 ? (
        <EmptyState message="No videos are tracked on this channel yet. Track a published video by its URL to see its figures after the next sync." />
      ) : (
        <>
          <VirtualTable
            rows={rows}
            columns={columns}
            getRowId={(row) => `analytics-video-${row.videoId}`}
            activeIndex={activeIndex}
            onActiveIndexChange={setActiveIndex}
            onRowActivate={openVideo}
            ariaLabel="Tracked videos. Press Enter to open a video."
          />
          <div className="flex items-center gap-2">
            {videos.hasNextPage && (
              <Button size="sm" onClick={() => void videos.fetchNextPage()} disabled={videos.isFetchingNextPage}>
                Load more
              </Button>
            )}
            {rows[activeIndex] && (
              <Button size="sm" variant="ghost" onClick={() => openVideo(rows[activeIndex])}>
                Open "{rows[activeIndex].title}"
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  );
}
