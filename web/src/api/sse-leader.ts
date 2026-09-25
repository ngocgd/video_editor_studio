/**
 * Leader election for the SSE bridge (guidelines/RT#2): one EventSource per
 * browser, not per tab. The tab that wins `navigator.locks.request('lt-sse')`
 * owns the actual stream and fans events out over a BroadcastChannel; every
 * tab (leader included) listens on the same channel for its own events.
 * When the leader tab closes, the lock is released and another tab picks it
 * up automatically, re-establishing the stream.
 *
 * Fallback: if the Web Locks API or BroadcastChannel is unavailable, every
 * tab runs its own stream (still bounded by the server's 6-stream-per-user
 * cap, see StreamEventsErrors 429 in openapi/paths/events.yaml).
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
 * Requests the leader lock and holds it until `onAcquired`'s returned
 * cleanup is invoked or the tab unloads. `onAcquired` is called once this
 * tab becomes the leader; it should start the stream and return a function
 * that stops it. Resolves once the lock request has been issued (the lock
 * itself is held for the browser session, not awaited here).
 */
export function electLeader(onAcquired: () => () => void): void {
  if (!locksSupported()) {
    return;
  }

  navigator.locks.request(LOCK_NAME, { mode: "exclusive" }, () => {
    const release = onAcquired();
    return new Promise<void>((resolve) => {
      window.addEventListener("beforeunload", () => {
        release();
        resolve();
      });
      // The lock is held until this promise resolves, which only happens on
      // unload; a tab that becomes leader stays leader until it closes.
    });
  });
}
