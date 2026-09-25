import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { getGpuStatusOptions, listJobsOptions } from "../../api/gen/@tanstack/react-query.gen";
import { cancelRun, cancelStep, retryStep } from "../../api/gen/sdk.gen";
import type { PipelineStep } from "../../api/gen/types.gen";
import { useSseTopics } from "../../api/use-sse-topics";

export type JobStatusFilter = "all" | "running" | "queued" | "failed" | "done";

/** Non-terminal steps are the only ones that can still change, so only their run ids are worth an SSE subscription (review H2). */
const NON_TERMINAL_STATUSES = new Set<PipelineStep["status"]>(["pending", "queued", "running"]);

/**
 * Fetches the job (step) list and subscribes the SSE bridge to every
 * *non-terminal* run id currently visible, so live progress patches the
 * cache in place instead of a page refetch (guidelines: "no page refetch").
 * A 15s poll stays on as a safety net for anything the SSE cap (H2) or a
 * degraded stream (H1) leaves uncovered.
 */
export function useJobs(status: JobStatusFilter) {
  const query = useQuery({
    ...listJobsOptions({ query: { status: status === "all" ? undefined : status, limit: 100 } }),
    refetchInterval: 15_000,
  });

  const jobs = query.data?.items ?? [];
  const liveRunIds = [...new Set(jobs.filter((job) => NON_TERMINAL_STATUSES.has(job.status)).map((job) => job.runId))];
  const { ready } = useSseTopics(liveRunIds);

  return { ...query, jobs, ready };
}

/** GPU status is polled (the real /events API has no gpu topic, see the phase report), paused while the tab is hidden. */
export function useGpuStatus() {
  return useQuery({
    ...getGpuStatusOptions(),
    refetchInterval: () => (typeof document !== "undefined" && document.hidden ? false : 4000),
  });
}

function reportMutationError(action: string, error: unknown): void {
  const detail = error instanceof Error ? error.message : "Please try again.";
  void import("sonner").then(({ toast }) => toast.error(`${action} failed`, { description: detail }));
}

function invalidateJobs(queryClient: ReturnType<typeof useQueryClient>): void {
  void queryClient.invalidateQueries({ predicate: (q) => matchesId(q.queryKey, "listJobs") });
}

export function useRetryStep() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (stepId: string) => {
      await retryStep({ path: { id: stepId }, throwOnError: true });
    },
    onSuccess: () => invalidateJobs(queryClient),
    onError: (error) => reportMutationError("Retry", error),
  });
}

export function useCancelStep() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (stepId: string) => {
      await cancelStep({ path: { id: stepId }, throwOnError: true });
    },
    onSuccess: () => invalidateJobs(queryClient),
    onError: (error) => reportMutationError("Cancel", error),
  });
}

export function useCancelRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (runId: string) => {
      await cancelRun({ path: { id: runId }, throwOnError: true });
    },
    onSuccess: () => invalidateJobs(queryClient),
    onError: (error) => reportMutationError("Cancel", error),
  });
}

function matchesId(queryKey: readonly unknown[], id: string): boolean {
  const first = queryKey[0];
  return typeof first === "object" && first !== null && (first as { _id?: string })._id === id;
}

export function gpuQueuePosition(job: PipelineStep, gpuQueueIds: string[]): number | undefined {
  const index = gpuQueueIds.indexOf(job.id);
  return index === -1 ? undefined : index + 1;
}
