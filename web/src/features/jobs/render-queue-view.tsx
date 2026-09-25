import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { listJobsOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { PipelineStep } from "../../api/gen/types.gen";
import { useSseTopics } from "../../api/use-sse-topics";
import { Button } from "../../components/ui/button";
import { EmptyState } from "../../components/shared/empty-state";
import { type EntityState, StatusChip } from "../../components/shared/status-chip";
import { VirtualTable, type VirtualTableColumn } from "../../components/shared/virtual-table";
import { formatEta, formatPercent } from "../../lib/format";
import { useCancelStep, useRetryStep, type JobStatusFilter } from "./use-jobs";

const FILTERS: Array<{ value: JobStatusFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "running", label: "Running" },
  { value: "queued", label: "Queued" },
  { value: "failed", label: "Failed" },
  { value: "done", label: "Done" },
];

function toEntityState(status: PipelineStep["status"]): EntityState {
  if (status === "canceled") return "failed";
  if (status === "pending") return "none";
  return status;
}

export function RenderQueueView() {
  const [filter, setFilter] = useState<JobStatusFilter>("all");
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [items, setItems] = useState<PipelineStep[]>([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const retryStep = useRetryStep();
  const cancelStep = useCancelStep();

  const query = useQuery({
    ...listJobsOptions({
      query: { status: filter === "all" ? undefined : filter, cursor, limit: 50 },
    }),
    refetchInterval: 15_000,
  });

  useEffect(() => {
    setCursor(undefined);
    setItems([]);
  }, [filter]);

  useEffect(() => {
    if (!query.data) return;
    setItems((prev) => {
      const seen = new Set(prev.map((item) => item.id));
      const fresh = query.data.items.filter((item) => !seen.has(item.id));
      return cursor ? [...prev, ...fresh] : query.data.items;
    });
    // cursor identifies which page just resolved; re-running on every
    // query.data change (not cursor) would drop the accumulation logic.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query.data]);

  const runIds = [...new Set(items.map((item) => item.runId))];
  useSseTopics(runIds);

  const columns: VirtualTableColumn<PipelineStep>[] = [
    { key: "kind", header: "Step", render: (row) => row.kind, className: "flex-[2] px-3" },
    {
      key: "status",
      header: "Status",
      render: (row) => <StatusChip state={toEntityState(row.status)} detail={row.status === "running" ? formatPercent(row.progress) : undefined} />,
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
        <div className="flex gap-1">
          {row.status === "failed" && (
            <Button variant="secondary" size="sm" onClick={() => retryStep.mutate(row.id)}>
              Retry
            </Button>
          )}
          {(row.status === "running" || row.status === "queued") && (
            <Button variant="ghost" size="sm" onClick={() => cancelStep.mutate(row.id)}>
              Cancel
            </Button>
          )}
        </div>
      ),
      className: "flex-1 px-3",
    },
  ];

  return (
    <div className="flex flex-col gap-3">
      <div role="tablist" aria-label="Filter jobs" className="flex gap-1">
        {FILTERS.map((option) => (
          <button
            key={option.value}
            type="button"
            role="tab"
            aria-selected={filter === option.value}
            onClick={() => setFilter(option.value)}
            className={`h-7 rounded-md px-2.5 text-xs ${
              filter === option.value ? "bg-primary text-primary-foreground" : "bg-secondary text-text-2 hover:text-foreground"
            }`}
          >
            {option.label}
          </button>
        ))}
      </div>
      {items.length === 0 && !query.isLoading ? (
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
      {query.data?.nextCursor && (
        <Button variant="secondary" size="sm" onClick={() => setCursor(query.data?.nextCursor)}>
          Load more
        </Button>
      )}
    </div>
  );
}
