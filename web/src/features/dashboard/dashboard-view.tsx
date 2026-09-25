import { useNavigate } from "@tanstack/react-router";

import { EmptyState } from "../../components/shared/empty-state";
import { InlineError } from "../../components/shared/inline-error";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { JobProgress } from "../../components/shared/job-progress";
import { gpuQueuePosition, useCancelStep, useGpuStatus, useJobs, useRetryStep } from "../jobs/use-jobs";

function StatValue({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-md border border-border bg-card px-3 py-2">
      <span className="text-2xs uppercase tracking-[0.04em] text-text-2">{label}</span>
      <span className="font-mono text-xl tabular-nums text-foreground">{value}</span>
    </div>
  );
}

function JobSkeleton() {
  return (
    <div className="flex flex-col gap-1.5" aria-hidden="true">
      <div className="h-4 w-40 animate-pulse rounded-sm bg-muted motion-reduce:animate-none" />
      <div className="h-1 w-full animate-pulse rounded-full bg-muted motion-reduce:animate-none" />
    </div>
  );
}

/** Dashboard (guidelines: running/queued jobs, GPU queue is the status bar's job, failed-with-retry). */
export function DashboardView() {
  const runningJobs = useJobs("running");
  const queuedJobs = useJobs("queued");
  const failedJobs = useJobs("failed");
  const gpuQuery = useGpuStatus();
  const retryStep = useRetryStep();
  const cancelStep = useCancelStep();
  const navigate = useNavigate();

  const gpuQueueIds = gpuQuery.data?.queue.map((step) => step.id) ?? [];
  const activeJobs = [...runningJobs.jobs, ...queuedJobs.jobs];
  const isLoading = runningJobs.isLoading || queuedJobs.isLoading;
  const isError = runningJobs.isError || queuedJobs.isError;

  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-3 gap-3">
        <StatValue label="Running" value={runningJobs.jobs.length} />
        <StatValue label="Queued" value={queuedJobs.jobs.length} />
        <StatValue label="Failed" value={failedJobs.jobs.length} />
      </div>

      <InspectorSection title="Active jobs">
        {isError ? (
          <InlineError
            cause="Could not load the active jobs list."
            onRetry={() => {
              void runningJobs.refetch();
              void queuedJobs.refetch();
            }}
          />
        ) : isLoading ? (
          <div className="flex flex-col gap-4">
            <JobSkeleton />
            <JobSkeleton />
          </div>
        ) : activeJobs.length === 0 ? (
          <EmptyState
            message="No jobs are running or queued right now. Start a render from the render queue to see it here."
            actionLabel="Open render queue"
            onAction={() => void navigate({ to: "/jobs" })}
          />
        ) : (
          <div className="flex flex-col gap-3">
            {activeJobs.map((job) => (
              <JobProgress
                key={job.id}
                kind={job.kind}
                status={job.status}
                progress={job.progress}
                etaS={job.etaS}
                gpuQueuePosition={gpuQueuePosition(job, gpuQueueIds)}
                onCancel={job.status === "running" ? () => cancelStep.mutate(job.id) : undefined}
              />
            ))}
          </div>
        )}
      </InspectorSection>

      {failedJobs.jobs.length > 0 && (
        <InspectorSection title="Failed">
          <div className="flex flex-col gap-3">
            {failedJobs.jobs.map((job) => (
              <JobProgress
                key={job.id}
                kind={job.kind}
                status={job.status}
                progress={job.progress}
                errorDetail={job.errorMsg}
                onRetry={() => retryStep.mutate(job.id)}
              />
            ))}
          </div>
        </InspectorSection>
      )}
    </div>
  );
}
