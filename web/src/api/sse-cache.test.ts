import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { PipelineStep, PipelineStepList } from "./gen/types.gen";
import { SseCachePatcher, type StepEvent } from "./sse-cache";

function makeStep(id: string, version: number): PipelineStep {
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
    version,
    remainingDeps: 0,
    progress: 0,
    createdAt: new Date().toISOString(),
  };
}

function makeEvent(id: string, version: number, overrides: Partial<StepEvent> = {}): StepEvent {
  return {
    type: "step",
    id,
    run_id: "run-1",
    tenant_id: "tenant-1",
    status: "running",
    version,
    transition: false,
    progress: version,
    ...overrides,
  };
}

describe("SseCachePatcher", () => {
  let queryClient: QueryClient;
  let rafCallback: FrameRequestCallback | undefined;
  let rafScheduleCount: number;

  beforeEach(() => {
    queryClient = new QueryClient();
    rafCallback = undefined;
    rafScheduleCount = 0;
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafScheduleCount += 1;
      rafCallback = cb;
      return 1;
    });
  });

  it("schedules one animation frame for 500 events pushed in the same tick, applying only the final state", () => {
    const patcher = new SseCachePatcher(queryClient);
    const listKey = [{ _id: "listJobs", baseUrl: "x" }];
    const list: PipelineStepList = { items: [makeStep("step-1", 0)] };
    queryClient.setQueryData(listKey, list);

    for (let version = 1; version <= 500; version += 1) {
      patcher.enqueue(makeEvent("step-1", version));
    }

    // Every enqueue before the frame fires reuses the same pending flush
    // (guidelines: "One SSE event batch leads to <=1 React commit per frame").
    expect(rafScheduleCount).toBe(1);

    // Nothing applied yet: the flush only runs once the animation frame fires.
    expect(queryClient.getQueryData<PipelineStepList>(listKey)?.items[0].version).toBe(0);

    rafCallback?.(0);

    const patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].progress).toBe(500);
    expect(patched?.items[0].version).toBe(500);
  });

  it("discards an out-of-order progress-only event but always applies a transition", () => {
    const patcher = new SseCachePatcher(queryClient);
    const listKey = [{ _id: "listJobs", baseUrl: "x" }];
    queryClient.setQueryData(listKey, { items: [makeStep("step-1", 0)] } satisfies PipelineStepList);

    patcher.enqueue(makeEvent("step-1", 5));
    rafCallback?.(0);
    patcher.enqueue(makeEvent("step-1", 3)); // stale, non-transition: ignored
    rafCallback?.(0);

    let patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].version).toBe(5);

    patcher.enqueue(makeEvent("step-1", 3, { transition: true, status: "failed" })); // always applied
    rafCallback?.(0);

    patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].status).toBe("failed");
  });
});
