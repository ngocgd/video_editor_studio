import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState } from "react";

import { getSseBridge } from "./sse-bridge";

/**
 * Subscribes this component to the given pipeline run ids for the SSE
 * bridge's lifetime and reports whether the stream has emitted its "ready"
 * event yet. Callers must gate their initial snapshot fetch on `ready`
 * (subscribe-before-snapshot, RT#2) via `enabled: ready` on the query.
 */
export function useSseTopics(topics: string[]): { ready: boolean } {
  const queryClient = useQueryClient();
  const consumerId = useId();
  const [ready, setReady] = useState(() => getSseBridge(queryClient).isReady());
  const topicsKey = topics.slice().sort().join(",");

  useEffect(() => {
    const bridge = getSseBridge(queryClient);
    const unsubscribe = bridge.subscribe(consumerId, topicsKey ? topicsKey.split(",") : []);
    const unsubscribeReady = bridge.onReady(() => setReady(true));
    setReady(bridge.isReady());
    return () => {
      unsubscribe();
      unsubscribeReady();
    };
    // topicsKey is the stable, order-independent identity of `topics`.
  }, [queryClient, consumerId, topicsKey]);

  return { ready };
}
