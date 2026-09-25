import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as sdkGen from "./gen/sdk.gen";
import { SseConnection } from "./sse-connection";
import { createStreamEventsMock } from "./sse-test-fakes";

describe("SseConnection", () => {
  let mock: ReturnType<typeof createStreamEventsMock>;

  beforeEach(() => {
    vi.useFakeTimers();
    // Deterministic backoff: the jitter itself is exercised by inspection
    // of jitteredBackoff's formula, not by asserting an exact delay here.
    vi.spyOn(Math, "random").mockReturnValue(0);
    mock = createStreamEventsMock();
    vi.spyOn(sdkGen, "streamEvents").mockImplementation(mock.streamEvents as unknown as typeof sdkGen.streamEvents);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  /** review H1: the server closes every stream after its max lifetime (1h); a fetch-based client must reconnect itself, unlike a browser EventSource. */
  it("reconnects after the server closes the stream cleanly, and the reconnect's ready is a resync point", async () => {
    const statuses: string[] = [];
    const onResync = vi.fn();
    const connection = new SseConnection({
      onEvent: () => {},
      onStatusChange: (status) => statuses.push(status),
      onResync,
    });

    connection.start("run-1");
    await vi.advanceTimersByTimeAsync(0);
    expect(mock.sessions).toHaveLength(1);
    mock.sessions[0].emitReady();
    expect(onResync).not.toHaveBeenCalled(); // the first ready is not a resync

    mock.sessions[0].closeClean();
    await vi.advanceTimersByTimeAsync(0);
    expect(statuses).toContain("degraded");

    // Backoff is full-jitter up to 1s on the first retry; 1100ms clears any jitter draw.
    await vi.advanceTimersByTimeAsync(1100);
    expect(mock.sessions).toHaveLength(2);

    mock.sessions[1].emitReady();
    expect(onResync).toHaveBeenCalledTimes(1); // the reconnect's ready IS a resync point
  });

  it("stops retrying on a 403 (an unknown/foreign topic) instead of reconnecting forever", async () => {
    const statuses: string[] = [];
    const connection = new SseConnection({ onEvent: () => {}, onStatusChange: (s) => statuses.push(s), onResync: () => {} });

    connection.start("run-unowned");
    await vi.advanceTimersByTimeAsync(0);
    mock.sessions[0].fail403();
    await vi.advanceTimersByTimeAsync(0);

    expect(statuses.at(-1)).toBe("degraded");
    await vi.advanceTimersByTimeAsync(60_000);
    // No further connection attempts: a 403 is not retried automatically.
    expect(mock.sessions).toHaveLength(1);
  });

  it("does not treat a deliberate stop() (topic change) as an error requiring reconnect", async () => {
    const connection = new SseConnection({ onEvent: () => {}, onStatusChange: () => {}, onResync: () => {} });
    connection.start("run-1");
    await vi.advanceTimersByTimeAsync(0);
    expect(mock.sessions).toHaveLength(1);

    connection.stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(mock.sessions).toHaveLength(1); // no reconnect attempt after an explicit stop
  });
});
