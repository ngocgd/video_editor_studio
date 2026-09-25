/**
 * Cross-tab auth sync (review M5): `switchTenant` rotates the session and
 * its CSRF token server-side, so every other open tab is holding a dead
 * token and stale tenant-scoped data until it happens to get a 401. This
 * channel lets the tab that changed auth state tell every other tab to
 * clear its cache and refetch immediately instead of waiting for that.
 */
const CHANNEL_NAME = "lt-auth";

function channel(): BroadcastChannel | null {
  return typeof BroadcastChannel !== "undefined" ? new BroadcastChannel(CHANNEL_NAME) : null;
}

/** Call after login, logout or switch-tenant succeeds. */
export function broadcastAuthChanged(): void {
  channel()?.postMessage({ kind: "auth-changed" });
  // BroadcastChannel does not deliver to the sending context itself, and a
  // short-lived channel instance here would miss the reply anyway, so
  // there is nothing further to close: GC reclaims it once idle.
}

/** Registers a listener for another tab's auth change; returns an unsubscribe function. */
export function onAuthChanged(listener: () => void): () => void {
  const bc = channel();
  if (!bc) return () => {};
  const handler = (event: MessageEvent<{ kind: string }>) => {
    if (event.data?.kind === "auth-changed") listener();
  };
  bc.addEventListener("message", handler);
  return () => {
    bc.removeEventListener("message", handler);
    bc.close();
  };
}
