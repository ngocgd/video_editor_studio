import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { listJobsOptions } from "../../api/gen/@tanstack/react-query.gen";
import { cancelRun, cancelStep, retryStep } from "../../api/gen/sdk.gen";
import type { PipelineStep } from "../../api/gen/types.gen";
import { useSseTopics } from "../../api/use-sse-topics";

export type JobStatusFilter = "all" | "running" | "queued" | "failed" | "done";

/**
 * Fetches the job (step) list and subscribes the SSE bridge to every run id
 * currently visible, so live progress patches the cache in place instead of
 * a page refetch (guidelines: "no page refetch").
 */
export function useJobs(status: JobStatusFilter) {
  const query = useQuery({
    ...listJobsOptions({ query: { status: status === "all" ? undefined : status, limit: 100 } }),
    refetchInterval: 15_000,
  });

  const jobs = query.data?.items ?? [];
  const runIds = [...new Set(jobs.map((job) => job.runId))];
  const { ready } = useSseTopics(runIds);

  return { ...query, jobs, ready };
}

export function useRetryStep() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (stepId: string) => {
      await retryStep({ path: { id: stepId }, throwOnError: true });
    },
    onSuccess: () => void queryClient.invalidateQueries({ predicate: (q) => matchesId(q.queryKey, "listJobs") }),
  });
}

export function useCancelStep() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (stepId: string) => {
      await cancelStep({ path: { id: stepId }, throwOnError: true });
    },
    onSuccess: () => void queryClient.invalidateQueries({ predicate: (q) => matchesId(q.queryKey, "listJobs") }),
  });
}

export function useCancelRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (runId: string) => {
      await cancelRun({ path: { id: runId }, throwOnError: true });
    },
    onSuccess: () => void queryClient.invalidateQueries({ predicate: (q) => matchesId(q.queryKey, "listJobs") }),
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
