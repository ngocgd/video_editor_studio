import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import type { PipelineStepList } from "./gen/types.gen";
import { humanizeKind } from "../lib/format";
import { getSseBridge } from "./sse-bridge";
import type { StepEvent } from "./sse-cache";

const ANNOUNCEMENT_TTL_MS = 8000;

/** Looks the completed step's human-readable kind up from whatever list cache already has it, since the SSE event payload itself only carries the step id. */
function findStepKind(queryClient: QueryClient, stepId: string): string | undefined {
  for (const query of queryClient.getQueryCache().findAll()) {
    const first = query.queryKey[0];
    const id = typeof first === "object" && first !== null ? (first as { _id?: string })._id : undefined;
    if (id !== "listJobs" && id !== "listRunSteps") continue;
    const data = query.state.data as PipelineStepList | InfiniteData<PipelineStepList> | undefined;
    if (!data) continue;
    const lists = "pages" in data ? data.pages : [data];
    for (const list of lists) {
      const match = list.items.find((item) => item.id === stepId);
      if (match) return match.kind;
    }
  }
  return undefined;
}

/** A short-lived text for the status bar's `aria-live` region, set only on a real job completion (guidelines: "for job completion"), never on a routine progress tick. */
export function useJobCompletionAnnouncement(): string {
  const queryClient = useQueryClient();
  const [message, setMessage] = useState("");

  useEffect(() => {
    const bridge = getSseBridge(queryClient);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = bridge.onCompletion((evt: StepEvent) => {
      const kind = findStepKind(queryClient, evt.id);
      const name = kind ? humanizeKind(kind) : "A job";
      const verb = evt.status === "done" ? "finished" : evt.status === "failed" ? "failed" : "was canceled";
      setMessage(`${name} ${verb}`);
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => setMessage(""), ANNOUNCEMENT_TTL_MS);
    });
    return () => {
      unsubscribe();
      if (timer) clearTimeout(timer);
    };
  }, [queryClient]);

  return message;
}
