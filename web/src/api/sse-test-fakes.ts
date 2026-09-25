/**
 * Test-only fakes for the SSE bridge's two browser dependencies
 * (BroadcastChannel and the Web Locks API) plus a controllable stand-in for
 * the generated `streamEvents` client, shared by sse-bridge.test.ts.
 */
import { vi } from "vitest";

/** An in-memory BroadcastChannel: instances sharing a `name` see each other's posts, never their own (matching the real API). */
export class FakeBroadcastChannel {
  private static readonly groups = new Map<string, Set<FakeBroadcastChannel>>();
  private readonly listeners = new Set<(event: { data: unknown }) => void>();

  constructor(private readonly name: string) {
    const group = FakeBroadcastChannel.groups.get(name) ?? new Set();
    group.add(this);
    FakeBroadcastChannel.groups.set(name, group);
  }

  postMessage(data: unknown): void {
    const group = FakeBroadcastChannel.groups.get(this.name);
    if (!group) return;
    for (const channel of group) {
      if (channel === this) continue;
      for (const listener of channel.listeners) listener({ data });
    }
  }

  addEventListener(_type: "message", listener: (event: { data: unknown }) => void): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: "message", listener: (event: { data: unknown }) => void): void {
    this.listeners.delete(listener);
  }

  close(): void {
    FakeBroadcastChannel.groups.get(this.name)?.delete(this);
  }

  static reset(): void {
    FakeBroadcastChannel.groups.clear();
  }
}

/** A single-holder exclusive lock queue mirroring navigator.locks' semantics, plus a `simulateTabClose` a browser tab-close would trigger (releasing without the holder's promise ever resolving). */
export class FakeLockManager {
  private holding = false;
  private readonly queue: Array<() => void> = [];
  private currentRelease: (() => void) | null = null;

  request(_name: string, _options: unknown, callback: () => Promise<void>): Promise<void> {
    return new Promise((resolve) => {
      const attempt = () => {
        this.holding = true;
        this.currentRelease = () => {
          this.holding = false;
          this.currentRelease = null;
          const next = this.queue.shift();
          resolve();
          if (next) next();
        };
        void callback(); // never resolves in production; the test releases explicitly
      };
      if (this.holding) {
        this.queue.push(attempt);
      } else {
        attempt();
      }
    });
  }

  /** Simulates the current leader tab closing: the browser force-releases the lock immediately. */
  simulateTabClose(): void {
    this.currentRelease?.();
  }
}

export interface FakeStreamSession {
  topicsKey: string;
  emitReady: () => void;
  emitStep: (data: unknown) => void;
  closeClean: () => void;
  fail403: () => void;
}

/** A controllable stand-in for `streamEvents`; each call opens one "session" the test can drive. */
export function createStreamEventsMock() {
  const sessions: FakeStreamSession[] = [];
  const onOpen = vi.fn();

  const streamEvents = vi.fn(
    async ({
      query,
      onSseEvent,
      onSseError,
    }: {
      query: { topics: string };
      onSseEvent: (event: { event: string; data: unknown }) => void;
      onSseError: (error: unknown) => void;
    }) => {
      let resolveNext: ((value: { done: boolean }) => void) | null = null;
      let ended = false;

      const session: FakeStreamSession = {
        topicsKey: query.topics,
        emitReady: () => onSseEvent({ event: "ready", data: null }),
        emitStep: (data) => onSseEvent({ event: "step", data }),
        closeClean: () => {
          ended = true;
          resolveNext?.({ done: true });
        },
        fail403: () => {
          onSseError(new Error("SSE failed: 403 Forbidden"));
          ended = true;
          resolveNext?.({ done: true });
        },
      };
      sessions.push(session);
      onOpen(session);

      const stream = {
        [Symbol.asyncIterator]: () => ({
          next: () =>
            ended
              ? Promise.resolve({ done: true, value: undefined })
              : new Promise<{ done: boolean; value?: undefined }>((resolve) => {
                  resolveNext = resolve;
                }),
        }),
      };
      return { stream };
    },
  );

  return { streamEvents, sessions, onOpen };
}
