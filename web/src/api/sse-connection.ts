import { streamEvents } from "./gen/sdk.gen";
import type { StreamEvent } from "./gen/core/serverSentEvents.gen";

export type StreamStatus = "idle" | "connecting" | "connected" | "degraded";

const RECONNECT_BASE_MS = 1000;
const RECONNECT_MAX_MS = 30_000;

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

/** Full-jitter backoff: a random delay up to min(cap, base * 2^attempt). */
function jitteredBackoff(attempt: number): number {
  const cap = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** attempt);
  return Math.random() * cap;
}

export interface SseConnectionHandlers {
  onEvent: (event: StreamEvent<unknown>) => void;
  onStatusChange: (status: StreamStatus) => void;
  /** Fires on every "ready" after the first one on this topic set (a reconnect resync point, review M2). */
  onResync: () => void;
}

/**
 * Owns one topic set's connection lifecycle end to end: the generated
 * client's own retry loop is disabled (`sseMaxRetryAttempts: 0`) so this
 * class fully decides what a clean server close (H1, the server closes
 * every stream after its max lifetime), a transient error (M2) and a
 * non-retryable 403 (H2, an unknown/foreign topic) each mean.
 */
export class SseConnection {
  private readonly handlers: SseConnectionHandlers;
  private abort: AbortController | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private generation = 0;
  private reconnectAttempt = 0;
  private hasConnectedBefore = false;

  constructor(handlers: SseConnectionHandlers) {
    this.handlers = handlers;
  }

  /** Starts (or restarts, for a new topic set) the connection loop. */
  start(topicsKey: string): void {
    this.stop();
    this.hasConnectedBefore = false;
    this.reconnectAttempt = 0;
    void this.runLoop(topicsKey, ++this.generation);
  }

  /** Aborts the current connection and stops reconnecting until `start` is called again. */
  stop(): void {
    this.generation += 1; // invalidates any in-flight loop iteration
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.abort?.abort();
    this.abort = null;
  }

  private async runLoop(topicsKey: string, generation: number): Promise<void> {
    while (this.generation === generation) {
      const controller = new AbortController();
      this.abort = controller;
      this.handlers.onStatusChange("connecting");

      const outcome = await this.connectOnce(topicsKey, controller);
      if (this.generation !== generation || controller.signal.aborted) {
        return; // Superseded by a new topic set or an explicit stop().
      }
      if (outcome === "forbidden") {
        this.handlers.onStatusChange("degraded");
        return; // Non-retryable: caller must call start() again with a new topic set.
      }

      // A clean close (H1) and a transient error (M2) both mean
      // "disconnected, should reconnect" with full-jitter backoff.
      this.handlers.onStatusChange("degraded");
      this.reconnectAttempt += 1;
      const delay = jitteredBackoff(this.reconnectAttempt);
      await new Promise<void>((resolve) => {
        this.reconnectTimer = setTimeout(resolve, delay);
      });
    }
  }

  private async connectOnce(topicsKey: string, controller: AbortController): Promise<"closed" | "error" | "forbidden"> {
    let forbidden = false;
    try {
      const { stream } = await streamEvents({
        query: { topics: topicsKey },
        signal: controller.signal,
        sseMaxRetryAttempts: 0,
        onSseEvent: (event) => {
          if (event.event === "ready") {
            this.handlers.onStatusChange("connected");
            if (this.hasConnectedBefore) this.handlers.onResync();
            this.hasConnectedBefore = true;
            // A successful (re)connect resets the backoff counter (review
            // M2: "attempt never resets after a successful connect"), so a
            // long-lived session that reconnects occasionally still starts
            // each new backoff from the 1s floor, not the 30s ceiling.
            this.reconnectAttempt = 0;
          }
          this.handlers.onEvent(event);
        },
        onSseError: (error) => {
          if (error instanceof Error && /\s403\s/.test(error.message)) {
            forbidden = true;
          }
        },
      });
      const iterator = stream[Symbol.asyncIterator]();
      while (!controller.signal.aborted) {
        const { done } = await iterator.next();
        if (done) break;
      }
    } catch (error) {
      if (isAbortError(error)) return "closed";
      return forbidden ? "forbidden" : "error";
    }
    return forbidden ? "forbidden" : "closed";
  }
}
