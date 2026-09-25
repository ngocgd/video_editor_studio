import type { Query, QueryClient } from "@tanstack/react-query";

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

/**
 * Applies step events to the query cache: batched once per animation frame,
 * gated by per-step version so an out-of-order event never regresses a
 * newer one, and always applied when it is a state transition (guidelines
 * "State transitions and terminal events are always applied").
 */
export class SseCachePatcher {
  private readonly queryClient: QueryClient;
  private readonly lastAppliedVersion = new Map<string, number>();
  private readonly pendingEvents: StepEvent[] = [];
  private rafHandle: number | null = null;

  constructor(queryClient: QueryClient) {
    this.queryClient = queryClient;
  }

  enqueue(evt: StepEvent): void {
    const versionKey = `step:${evt.id}`;
    const lastVersion = this.lastAppliedVersion.get(versionKey) ?? -1;
    if (evt.version <= lastVersion && !evt.transition) return;
    this.lastAppliedVersion.set(versionKey, evt.version);
    this.pendingEvents.push(evt);
    this.scheduleFlush();
  }

  reset(): void {
    this.lastAppliedVersion.clear();
  }

  private scheduleFlush(): void {
    if (this.rafHandle != null) return;
    this.rafHandle = requestAnimationFrame(() => this.flush());
  }

  private flush(): void {
    this.rafHandle = null;
    const events = this.pendingEvents.splice(0, this.pendingEvents.length);
    for (const evt of events) {
      this.patchStepCaches(evt);
    }
  }

  private patchStepCaches(evt: StepEvent): void {
    const patch = (step: PipelineStep): PipelineStep => ({
      ...step,
      status: evt.status,
      version: evt.version,
      progress: evt.progress ?? step.progress,
      etaS: evt.eta_s ?? step.etaS,
    });

    this.queryClient.setQueryData<PipelineStep>(["step", evt.id], (old) => (old ? patch(old) : old));

    for (const id of ["listJobs", "listRunSteps"] as const) {
      this.queryClient.setQueriesData<PipelineStepList>(
        { predicate: (query) => matchesQueryId(query, id) },
        (old) => {
          if (!old) return old;
          const index = old.items.findIndex((item) => item.id === evt.id);
          if (index === -1) return old;
          const items = old.items.slice();
          items[index] = patch(items[index]);
          return { ...old, items };
        },
      );
    }
  }
}
