/**
 * Leader election for the SSE bridge (guidelines/RT#2): one EventSource per
 * browser, not per tab. The tab that wins `navigator.locks.request('lt-sse')`
 * owns the actual stream and fans events out over a BroadcastChannel; every
 * tab (leader included) listens on the same channel for its own events.
 * When the leader tab closes, the browser releases the lock automatically
 * (a lock's holding promise never has to resolve for that to happen), and
 * another waiting tab is granted it, re-establishing the stream.
 *
 * Fallback: if the Web Locks API is unavailable, every tab runs its own
 * stream (still bounded by the server's 6-stream-per-user cap, see
 * StreamEventsErrors 429 in openapi/paths/events.yaml).
 */

export const LOCK_NAME = "lt-sse";
export const CHANNEL_NAME = "lt-events";

export function locksSupported(): boolean {
  return typeof navigator !== "undefined" && "locks" in navigator;
}

export function broadcastChannelSupported(): boolean {
  return typeof BroadcastChannel !== "undefined";
}

/**
 * Requests the leader lock; `onAcquired` runs once this tab wins it and
 * should start the stream. Without Web Locks support every tab calls
 * `onAcquired` immediately (the documented fallback). A tab that becomes
 * leader stays leader until the browser tears down its JS context (tab
 * close/navigation), which is also when the lock and the stream both go
 * away together — there is deliberately no `beforeunload` listener here
 * (it would only race the browser's own release and would defeat bfcache).
 */
export function electLeader(onAcquired: () => void): void {
  if (!locksSupported()) {
    onAcquired();
    return;
  }

  void navigator.locks.request(LOCK_NAME, { mode: "exclusive" }, () => {
    onAcquired();
    return new Promise<void>(() => {
      // Intentionally never resolves: held for the tab's lifetime.
    });
  });
}
