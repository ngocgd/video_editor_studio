import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";

import { listJobsInfiniteOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { PipelineStep } from "../../api/gen/types.gen";
import { useSseTopics } from "../../api/use-sse-topics";
import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { type EntityState, StatusChip } from "../../components/shared/status-chip";
import { VirtualTable, type VirtualTableColumn } from "../../components/shared/virtual-table";
import { Button } from "../../components/ui/button";
import { formatEta, formatPercent, humanizeKind } from "../../lib/format";
import { useCancelStep, useRetryStep, type JobStatusFilter } from "./use-jobs";

const FILTERS: Array<{ value: JobStatusFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "running", label: "Running" },
  { value: "queued", label: "Queued" },
  { value: "failed", label: "Failed" },
  { value: "done", label: "Done" },
];

const NON_TERMINAL = new Set<PipelineStep["status"]>(["pending", "queued", "running"]);

function toEntityState(status: PipelineStep["status"]): EntityState {
  if (status === "canceled") return "failed";
  if (status === "pending") return "none";
  return status;
}

function RowActions({ row, confirmingCancel, onConfirmCancel, onCancel, onRetry }: {
  row: PipelineStep;
  confirmingCancel: boolean;
  onConfirmCancel: () => void;
  onCancel: () => void;
  onRetry: () => void;
}) {
  if (row.status === "failed") {
    return (
      <Button variant="secondary" size="sm" onClick={onRetry}>
        Retry
      </Button>
    );
  }
  if (row.status !== "running" && row.status !== "queued") return null;
  if (confirmingCancel) {
    return (
      <span className="flex items-center gap-1 text-xs text-text-2">
        Cancel?
        <Button variant="destructive" size="sm" onClick={onCancel}>
          Yes
        </Button>
      </span>
    );
  }
  return (
    <Button variant="ghost" size="sm" onClick={onConfirmCancel}>
      Cancel
    </Button>
  );
}

/**
 * `useInfiniteQuery` keeps every loaded page as its own live query (review
 * H5): the SSE bridge patches pages in place via `sse-cache.ts`'s
 * `InfiniteData` support, so a row from page 1 keeps updating after
 * "Load more" instead of going stale. Switching the filter changes the
 * query key, which resets pagination for free (no manual cursor reset).
 */
export function RenderQueueView() {
  const [filter, setFilter] = useState<JobStatusFilter>("all");
  const [activeIndex, setActiveIndex] = useState(0);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const retryStep = useRetryStep();
  const cancelStep = useCancelStep();

  const query = useInfiniteQuery({
    ...listJobsInfiniteOptions({ query: { status: filter === "all" ? undefined : filter, limit: 50 } }),
    initialPageParam: {},
    getNextPageParam: (lastPage) => lastPage.nextCursor,
    refetchInterval: 15_000,
  });

  const items = query.data?.pages.flatMap((page) => page.items) ?? [];
  const liveRunIds = [...new Set(items.filter((item) => NON_TERMINAL.has(item.status)).map((item) => item.runId))];
  useSseTopics(liveRunIds);

  const columns: VirtualTableColumn<PipelineStep>[] = [
    {
      key: "kind",
      header: "Step",
      render: (row) => <span title={row.kind}>{humanizeKind(row.kind)}</span>,
      className: "flex-[2] px-3",
    },
    {
      key: "status",
      header: "Status",
      render: (row) => (
        <StatusChip state={toEntityState(row.status)} detail={row.status === "running" ? formatPercent(row.progress) : undefined} />
      ),
      className: "flex-1 px-3",
    },
    {
      key: "eta",
      header: "ETA",
      render: (row) => <span className="font-mono text-xs tabular-nums">{formatEta(row.etaS) ?? "-"}</span>,
      className: "flex-1 px-3",
    },
    {
      key: "actions",
      header: "Actions",
      render: (row) => (
        <RowActions
          row={row}
          confirmingCancel={confirmingId === row.id}
          onConfirmCancel={() => setConfirmingId(row.id)}
          onCancel={() => {
            cancelStep.mutate(row.id);
            setConfirmingId(null);
          }}
          onRetry={() => retryStep.mutate(row.id)}
        />
      ),
      className: "flex-1 px-3",
    },
  ];

  return (
    <div className="flex flex-col gap-3">
      <div className="flex gap-1">
        {FILTERS.map((option) => (
          <button
            key={option.value}
            type="button"
            aria-pressed={filter === option.value}
            onClick={() => setFilter(option.value)}
            className={`h-7 rounded-md px-2.5 text-xs ${
              filter === option.value ? "bg-primary text-primary-foreground" : "bg-secondary text-text-2 hover:text-foreground"
            }`}
          >
            {option.label}
          </button>
        ))}
      </div>
      {query.isError ? (
        <InlineError cause="Could not load the render queue." onRetry={() => void query.refetch()} />
      ) : items.length === 0 && !query.isLoading ? (
        <EmptyState message="No jobs match this filter yet." />
      ) : (
        <VirtualTable
          rows={items}
          columns={columns}
          getRowId={(row) => row.id}
          activeIndex={activeIndex}
          onActiveIndexChange={setActiveIndex}
          ariaLabel="Render queue"
        />
      )}
      {query.hasNextPage && (
        <Button variant="secondary" size="sm" onClick={() => void query.fetchNextPage()} disabled={query.isFetchingNextPage}>
          {query.isFetchingNextPage ? "Loading..." : "Load more"}
        </Button>
      )}
    </div>
  );
}
