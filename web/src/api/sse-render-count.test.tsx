import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";

import type { PipelineStep } from "./gen/types.gen";
import { SseCachePatcher, type StepEvent } from "./sse-cache";

function makeStep(id: string): PipelineStep {
  return {
    id,
    runId: "run-1",
    scopeKind: "episode",
    scopeId: "ep-1",
    kind: "render",
    queue: "gpu",
    priority: 1,
    status: "running",
    attempt: 1,
    version: 0,
    remainingDeps: 0,
    progress: 0,
    createdAt: new Date().toISOString(),
  };
}

function makeEvent(version: number): StepEvent {
  return {
    type: "step",
    id: "step-1",
    run_id: "run-1",
    tenant_id: "tenant-1",
    status: "running",
    version,
    transition: false,
    progress: version % 100,
  };
}

function RenderCounter({ onRender }: { onRender: () => void }) {
  const { data } = useQuery<PipelineStep>({ queryKey: ["step", "step-1"], queryFn: () => Promise.resolve(makeStep("step-1")) });
  const renderCount = useRef(0);
  renderCount.current += 1;
  onRender();
  return <span data-testid="progress">{data?.progress}</span>;
}

describe("SSE batching keeps React commits bounded (guidelines: <=60 commits for 500 events/1s)", () => {
  it("produces at most a handful of commits for 500 events flushed across several animation frames", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity } } });
    queryClient.setQueryData(["step", "step-1"], makeStep("step-1"));
    const patcher = new SseCachePatcher(queryClient);

    let rafCallback: FrameRequestCallback | undefined;
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafCallback = cb;
      return 1;
    });

    let commitCount = 0;
    render(
      <QueryClientProvider client={queryClient}>
        <RenderCounter onRender={() => (commitCount += 1)} />
      </QueryClientProvider>,
    );
    const initialCommits = commitCount;

    // 500 events arriving across 10 animation frames (50 per frame), the
    // realistic shape of a burst, not all in one JS tick.
    for (let frame = 0; frame < 10; frame += 1) {
      for (let i = 0; i < 50; i += 1) {
        patcher.enqueue(makeEvent(frame * 50 + i + 1));
      }
      rafCallback?.(0);
    }

    // One commit per frame flush at most (10), well under the 60-commit
    // budget for 500 events, and coalescing means most frames only ever
    // write the single latest value for this one step.
    expect(commitCount - initialCommits).toBeLessThanOrEqual(10);
    vi.unstubAllGlobals();
  });
});
