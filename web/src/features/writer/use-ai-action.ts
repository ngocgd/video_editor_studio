import { useMutation, useQuery } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";

import { createAiActionMutation, getAiActionResultOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { AiActionRequest, AiActionResult } from "../../api/gen/types.gen";
import type { DiffSegment } from "../../components/shared/diff-proposal";
import { diffText } from "./text-diff";

/**
 * How often a pending AI action step is re-polled: quickly at first, then
 * backing off, so a long generation doesn't spend the per-IP request budget
 * that autosave also draws on.
 */
export const AI_ACTION_POLL_MS = 400;
export const AI_ACTION_POLL_MAX_MS = 2_000;

export function aiActionPollDelay(pollsSoFar: number): number {
  return Math.min(AI_ACTION_POLL_MS * 1.5 ** pollsSoFar, AI_ACTION_POLL_MAX_MS);
}

/** A rate-limited poll is retried after a pause instead of failing the proposal. */
function retryPoll(failureCount: number, error: unknown): boolean {
  if ((error as { status?: unknown } | null)?.status === 429) return failureCount < 5;
  return failureCount < 1;
}

const TERMINAL: ReadonlySet<AiActionResult["status"]> = new Set(["done", "failed", "canceled"]);

export interface AiProposal {
  action: AiActionRequest["action"];
  paragraphIds: string[];
  originalText: string;
  text: string;
  segments: DiffSegment[];
  /** True once the step finished with text; only then may the proposal be accepted. */
  done: boolean;
  /** Set when the step failed or was canceled (or finished without text). */
  error?: string;
  provider?: string;
  stepId: string;
}

interface PendingAction {
  action: AiActionRequest["action"];
  paragraphIds: string[];
  originalText: string;
  stepId: string;
}

/**
 * Runs one AI action (Continue/Rewrite/Expand/Shorten/Tone/Translate/
 * Summarise) and polls its step result until it reaches a terminal status.
 * The server returns the full generated text once the step is done (there
 * is no incremental token stream), which becomes a reviewable
 * `DiffProposal`. Nothing is applied until the caller calls the writer's
 * `applyOpsNow` on Accept.
 */
export function useAiAction(episodeId: string) {
  const create = useMutation(createAiActionMutation());
  const [pending, setPending] = useState<PendingAction | null>(null);

  const result = useQuery({
    ...getAiActionResultOptions({ path: { id: episodeId, stepId: pending?.stepId ?? "" } }),
    enabled: pending !== null,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status && TERMINAL.has(status) ? false : aiActionPollDelay(query.state.dataUpdateCount);
    },
    retry: retryPoll,
    retryDelay: AI_ACTION_POLL_MAX_MS,
    // Each run has a fresh step id, so a cached result is never reused.
    gcTime: 0,
  });

  const run = useCallback(
    (body: AiActionRequest, originalText: string) => {
      setPending(null);
      create.mutate(
        { path: { id: episodeId }, body },
        {
          onSuccess: (res) => {
            if (!res) return;
            setPending({ action: body.action, paragraphIds: body.paragraphIds ?? [], originalText, stepId: res.stepId });
          },
        },
      );
    },
    [episodeId, create],
  );

  const clear = useCallback(() => setPending(null), []);

  const proposal = useMemo<AiProposal | null>(() => {
    if (!pending) return null;
    const data = result.data;
    const base = { ...pending, text: "", segments: [] as DiffSegment[], done: false };
    if (result.error) return { ...base, error: "Could not load the AI result." };
    if (!data || !TERMINAL.has(data.status)) return base;
    if (data.status === "done" && data.text) {
      return { ...base, text: data.text, segments: diffText(pending.originalText, data.text), done: true, provider: data.provider };
    }
    const fallback = data.status === "canceled" ? "The AI action was canceled." : "The AI action failed.";
    return { ...base, error: data.errorDetail || fallback };
  }, [pending, result.data, result.error]);

  return { proposal, run, clear, isStarting: create.isPending, error: create.error as Error | null };
}
