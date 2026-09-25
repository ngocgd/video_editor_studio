import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { getGpuStatusOptions } from "../../api/gen/@tanstack/react-query.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { GpuStatusBar } from "../../components/shared/gpu-status-bar";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { JobProgress } from "../../components/shared/job-progress";
import { gpuQueuePosition, useCancelStep, useJobs, useRetryStep } from "../jobs/use-jobs";

/** Dashboard (guidelines: running/queued jobs, GPU queue, failed-with-retry). */
export function DashboardView() {
  const runningJobs = useJobs("running");
  const queuedJobs = useJobs("queued");
  const failedJobs = useJobs("failed");
  const gpuQuery = useQuery({ ...getGpuStatusOptions(), refetchInterval: 4000 });
  const retryStep = useRetryStep();
  const cancelStep = useCancelStep();

  const gpuQueueIds = gpuQuery.data?.queue.map((step) => step.id) ?? [];
  const activeJobs = [...runningJobs.jobs, ...queuedJobs.jobs];

  return (
    <div className="flex flex-col gap-6">
      <InspectorSection title="GPU">
        <GpuStatusBar status={gpuQuery.data} />
      </InspectorSection>

      <InspectorSection title="Active jobs">
        {activeJobs.length === 0 ? (
          <EmptyState
            message="No jobs are running or queued right now. Start a render from a project to see it here."
            actionLabel="Open render queue"
          />
        ) : (
          <div className="flex flex-col gap-3">
            {activeJobs.map((job) => (
              <JobProgress
                key={job.id}
                name={job.kind}
                step={job.status}
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
                name={job.kind}
                step={job.errorMsg ?? "Step failed"}
                status={job.status}
                progress={job.progress}
                onRetry={() => retryStep.mutate(job.id)}
              />
            ))}
          </div>
        </InspectorSection>
      )}

      <Link to="/jobs" className="text-sm text-primary-text hover:underline">
        View the full render queue
      </Link>
    </div>
  );
}
