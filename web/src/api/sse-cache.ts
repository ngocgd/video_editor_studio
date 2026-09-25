import { notifyManager, type InfiniteData, type Query, type QueryClient } from "@tanstack/react-query";

import type { PipelineStep, PipelineStepList } from "./gen/types.gen";

/** Matches the hey-api generated list query keys, e.g. `[{ _id: 'listJobs', ... }]`. */
function matchesQueryId(query: Query, id: string): boolean {
  const first = query.queryKey[0];
  return typeof first === "object" && first !== null && (first as { _id?: string })._id === id;
}

/** Decoded `event: step` payload (mirrors api/internal/sse.Event). */
export interface StepEvent {
  type: string;
  id: string;
  run_id: string;
  tenant_id: string;
  status: PipelineStep["status"];
  version: number;
  transition: boolean;
  progress?: number;
  eta_s?: number;
}

function isInfinite(value: PipelineStepList | InfiniteData<PipelineStepList>): value is InfiniteData<PipelineStepList> {
  return "pages" in value;
}

/**
 * Applies step events to the query cache: coalesced per step id (only the
 * highest-version pending event per step survives, which also keeps a
 * terminal event since it always carries the highest version in a legal
 * sequence) and flushed in one batch, so N events for the same step cost
 * one cache write, not N. Every write is individually gated by comparing
 * against *that cache entry's own* cached version (openapi/paths/events.yaml:
 * "discard any event whose version is <= the snapshot's"), not a private
 * counter, so a stale relayed event can never roll back a newer snapshot.
 */
const TERMINAL_STATUSES = new Set<PipelineStep["status"]>(["done", "failed", "canceled"]);

export class SseCachePatcher {
  private readonly queryClient: QueryClient;
  private readonly pending = new Map<string, StepEvent>();
  private readonly completionListeners = new Set<(evt: StepEvent) => void>();
  private flushHandle: number | ReturnType<typeof setTimeout> | null = null;

  constructor(queryClient: QueryClient) {
    this.queryClient = queryClient;
  }

  /** Fires for a job-completion announcement (guidelines: footer[role=status][aria-live=polite] "for job completion"), never for a routine progress tick. */
  onCompletion(listener: (evt: StepEvent) => void): () => void {
    this.completionListeners.add(listener);
    return () => this.completionListeners.delete(listener);
  }

  enqueue(evt: StepEvent): void {
    const existing = this.pending.get(evt.id);
    if (!existing || evt.version >= existing.version) {
      this.pending.set(evt.id, evt);
    }
    this.scheduleFlush();
  }

  reset(): void {
    this.pending.clear();
  }

  /**
   * On a resync (server LISTEN reconnect or our own stream reconnect),
   * invalidate the SSE-backed list snapshots without an immediate refetch,
   * then refetch only the active ones once the caller signals `ready`
   * again — the same "invalidate now, refetch after ready" sequencing the
   * bridge needs, kept next to the cache predicate it depends on.
   */
  invalidateSnapshots(onNextReady: (listener: () => void) => () => void): void {
    this.reset();
    const isSnapshotQuery = (query: Query) => matchesQueryId(query, "listJobs") || matchesQueryId(query, "listRunSteps");
    void this.queryClient.invalidateQueries({ predicate: isSnapshotQuery, refetchType: "none" });
    const unsubscribe = onNextReady(() => {
      unsubscribe();
      void this.queryClient.refetchQueries({ predicate: isSnapshotQuery, type: "active" });
    });
  }

  private scheduleFlush(): void {
    if (this.flushHandle != null) return;
    // requestAnimationFrame never fires in a hidden tab (H4), which would
    // let events queue without bound; fall back to a short timer there.
    if (typeof document !== "undefined" && document.hidden) {
      this.flushHandle = setTimeout(() => this.flush(), 250);
    } else {
      this.flushHandle = requestAnimationFrame(() => this.flush());
    }
  }

  private flush(): void {
    this.flushHandle = null;
    const events = [...this.pending.values()];
    this.pending.clear();
    // notifyManager.batch collapses every setQueryData/setQueriesData call
    // below into a single subscriber notification pass, so N patched steps
    // in one frame still cost at most one React commit per observer.
    notifyManager.batch(() => {
      for (const evt of events) {
        this.patchStepCaches(evt);
        if (evt.transition && TERMINAL_STATUSES.has(evt.status)) {
          for (const listener of this.completionListeners) listener(evt);
        }
      }
    });
  }

  private patchStepCaches(evt: StepEvent): void {
    const patchIfNewer = (step: PipelineStep): PipelineStep => {
      if (evt.version <= step.version) return step;
      return {
        ...step,
        status: evt.status,
        version: evt.version,
        progress: evt.progress ?? step.progress,
        etaS: evt.eta_s ?? step.etaS,
      };
    };

    this.queryClient.setQueryData<PipelineStep>(["step", evt.id], (old) => (old ? patchIfNewer(old) : old));

    const patchList = (list: PipelineStepList): PipelineStepList => {
      const index = list.items.findIndex((item) => item.id === evt.id);
      if (index === -1) return list;
      const patched = patchIfNewer(list.items[index]);
      if (patched === list.items[index]) return list;
      const items = list.items.slice();
      items[index] = patched;
      return { ...list, items };
    };

    for (const id of ["listJobs", "listRunSteps"] as const) {
      this.queryClient.setQueriesData<PipelineStepList | InfiniteData<PipelineStepList>>(
        { predicate: (query) => matchesQueryId(query, id) },
        (old) => {
          if (!old) return old;
          if (isInfinite(old)) {
            const pages = old.pages.map(patchList);
            return pages.some((page, i) => page !== old.pages[i]) ? { ...old, pages } : old;
          }
          return patchList(old);
        },
      );
    }
  }
}
