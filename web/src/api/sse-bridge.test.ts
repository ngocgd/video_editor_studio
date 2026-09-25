import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as sdkGen from "./gen/sdk.gen";
import { SseBridge } from "./sse-bridge";
import { createStreamEventsMock, FakeBroadcastChannel, FakeLockManager } from "./sse-test-fakes";

describe("SseBridge (multi-tab leader election)", () => {
  let lockManager: FakeLockManager;
  let mock: ReturnType<typeof createStreamEventsMock>;

  beforeEach(async () => {
    vi.useFakeTimers();
    FakeBroadcastChannel.reset();
    vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
    lockManager = new FakeLockManager();
    vi.stubGlobal("navigator", { locks: lockManager });

    // Re-mock streamEvents per test so each test gets fresh session tracking.
    mock = createStreamEventsMock();
    vi.spyOn(sdkGen, "streamEvents").mockImplementation(mock.streamEvents as unknown as typeof sdkGen.streamEvents);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  function makeTab() {
    return new SseBridge(new QueryClient());
  }

  it("elects exactly one leader across 3 tabs, opening exactly one connection for their combined topics", async () => {
    const tabA = makeTab();
    const tabB = makeTab();
    const tabC = makeTab();

    tabA.subscribe("consumerA", ["run-1"]);
    tabB.subscribe("consumerB", ["run-2"]);
    tabC.subscribe("consumerC", ["run-1", "run-3"]);

    await vi.advanceTimersByTimeAsync(300); // rebuild debounce

    expect(mock.streamEvents).toHaveBeenCalledTimes(1);
    expect(mock.sessions).toHaveLength(1);
    expect(mock.sessions[0].topicsKey.split(",").sort()).toEqual(["run-1", "run-2", "run-3"]);
  });

  it("re-establishes a single connection with a new leader after the leader tab closes", async () => {
    const tabA = makeTab();
    const tabB = makeTab();
    tabA.subscribe("a", ["run-1"]);
    tabB.subscribe("b", ["run-2"]);

    await vi.advanceTimersByTimeAsync(300);
    expect(mock.sessions).toHaveLength(1);
    mock.sessions[0].emitReady();

    // The leader tab closes; the browser force-releases the lock.
    lockManager.simulateTabClose();
    await vi.advanceTimersByTimeAsync(300);

    // A second, independent connection opens for the surviving tab's topics.
    expect(mock.sessions).toHaveLength(2);
    expect(mock.streamEvents).toHaveBeenCalledTimes(2);
  });

  it("patches every tab's query cache from one leader-owned stream (fan-out via BroadcastChannel)", async () => {
    const queryClientA = new QueryClient();
    const queryClientB = new QueryClient();
    const tabA = new SseBridge(queryClientA);
    const tabB = new SseBridge(queryClientB);

    const listKey = [{ _id: "listJobs", baseUrl: "x" }];
    const step = {
      id: "step-1",
      runId: "run-1",
      scopeKind: "episode",
      scopeId: "ep-1",
      kind: "render",
      queue: "gpu",
      priority: 1,
      status: "queued",
      attempt: 1,
      version: 1,
      remainingDeps: 0,
      progress: 0,
      createdAt: new Date().toISOString(),
    };
    queryClientA.setQueryData(listKey, { items: [step] });
    queryClientB.setQueryData(listKey, { items: [step] });

    tabA.subscribe("a", ["run-1"]);
    tabB.subscribe("b", ["run-1"]);
    await vi.advanceTimersByTimeAsync(300);

    mock.sessions[0].emitReady();
    mock.sessions[0].emitStep({
      type: "step",
      id: "step-1",
      run_id: "run-1",
      tenant_id: "t1",
      status: "running",
      version: 5,
      transition: true,
      progress: 50,
    });
    await vi.advanceTimersByTimeAsync(20); // rAF flush (jsdom shims it as a fast timer)

    expect(queryClientA.getQueryData<{ items: Array<{ status: string }> }>(listKey)?.items[0].status).toBe("running");
    expect(queryClientB.getQueryData<{ items: Array<{ status: string }> }>(listKey)?.items[0].status).toBe("running");
  });
});
