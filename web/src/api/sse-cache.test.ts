import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { PipelineStep, PipelineStepList } from "./gen/types.gen";
import { SseCachePatcher, type StepEvent } from "./sse-cache";

function setDocumentHidden(hidden: boolean): void {
  Object.defineProperty(document, "hidden", { value: hidden, configurable: true });
}

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
  const listKey = [{ _id: "listJobs", baseUrl: "x" }];

  beforeEach(() => {
    queryClient = new QueryClient();
    rafCallback = undefined;
    rafScheduleCount = 0;
    setDocumentHidden(false);
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
      rafScheduleCount += 1;
      rafCallback = cb;
      return 1;
    });
  });

  it("schedules one animation frame for 500 events on the same step in one tick, coalescing to the highest version", () => {
    const patcher = new SseCachePatcher(queryClient);
    queryClient.setQueryData(listKey, { items: [makeStep("step-1", 0)] } satisfies PipelineStepList);

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

  it("never regresses a newer cached version, even for a relayed transition event (openapi events.yaml: discard <= snapshot version)", () => {
    const patcher = new SseCachePatcher(queryClient);
    queryClient.setQueryData(listKey, { items: [makeStep("step-1", 0)] } satisfies PipelineStepList);

    patcher.enqueue(makeEvent("step-1", 5));
    rafCallback?.(0);
    patcher.enqueue(makeEvent("step-1", 3)); // stale, non-transition: ignored
    rafCallback?.(0);

    let patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].version).toBe(5);

    // A stale *transition* must not roll a newer snapshot back either: the
    // cache already reflects v5, so a relayed v3 "failed" is dropped.
    patcher.enqueue(makeEvent("step-1", 3, { transition: true, status: "failed" }));
    rafCallback?.(0);

    patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].version).toBe(5);
    expect(patched?.items[0].status).toBe("running");

    // A genuinely newer transition is still applied.
    patcher.enqueue(makeEvent("step-1", 6, { transition: true, status: "failed" }));
    rafCallback?.(0);
    patched = queryClient.getQueryData<PipelineStepList>(listKey);
    expect(patched?.items[0].status).toBe("failed");
  });

  it("patches an InfiniteData page (Render Queue's useInfiniteQuery cache) in place", () => {
    const patcher = new SseCachePatcher(queryClient);
    const infiniteKey = [{ _id: "listJobs", baseUrl: "x", _infinite: true }];
    const data: InfiniteData<PipelineStepList> = {
      pages: [{ items: [makeStep("step-1", 1)], nextCursor: "c1" }, { items: [makeStep("step-2", 1)] }],
      pageParams: [undefined, "c1"],
    };
    queryClient.setQueryData(infiniteKey, data);

    patcher.enqueue(makeEvent("step-2", 7, { transition: true, status: "done", progress: 100 }));
    rafCallback?.(0);

    const patched = queryClient.getQueryData<InfiniteData<PipelineStepList>>(infiniteKey);
    expect(patched?.pages[0].items[0].version).toBe(1);
    expect(patched?.pages[1].items[0].status).toBe("done");
    expect(patched?.pages[1].items[0].version).toBe(7);
  });

  it("falls back to a timer (not rAF, which never fires) when the tab is hidden", () => {
    setDocumentHidden(true);
    vi.useFakeTimers();
    const patcher = new SseCachePatcher(queryClient);
    queryClient.setQueryData(listKey, { items: [makeStep("step-1", 0)] } satisfies PipelineStepList);

    patcher.enqueue(makeEvent("step-1", 1));
    expect(rafScheduleCount).toBe(0);

    vi.advanceTimersByTime(250);

    expect(queryClient.getQueryData<PipelineStepList>(listKey)?.items[0].version).toBe(1);
    vi.useRealTimers();
  });
});
