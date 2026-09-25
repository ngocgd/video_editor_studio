import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";

import { createAiActionMutation } from "../../api/gen/@tanstack/react-query.gen";
import type { AiActionRequest } from "../../api/gen/types.gen";
import type { DiffSegment } from "../../components/shared/diff-proposal";
import { getSseBridge } from "../../api/sse-bridge";
import { useSseTopics } from "../../api/use-sse-topics";
import { diffText } from "./text-diff";

export interface AiProposal {
  action: AiActionRequest["action"];
  paragraphIds: string[];
  originalText: string;
  text: string;
  segments: DiffSegment[];
  done: boolean;
  stepId: string;
}

/**
 * Runs one AI action (phase 6: Outline/Continue/Rewrite/Expand/Shorten/Tone/
 * Translate/Summarise) and accumulates its `llm.delta` token stream (see
 * ../../api/sse-ai-delta for the assumed payload shape) into a reviewable
 * `DiffProposal`. Nothing is applied until the caller calls the writer's
 * `applyOpsNow` on Accept.
 */
export function useAiAction(episodeId: string) {
  const queryClient = useQueryClient();
  const create = useMutation(createAiActionMutation());
  const [proposal, setProposal] = useState<AiProposal | null>(null);
  const [runId, setRunId] = useState<string | null>(null);
  const unsubscribeRef = useRef<(() => void) | null>(null);

  // Keeps the shared SSE bridge subscribed to this run for the life of the streaming proposal.
  useSseTopics(runId ? [runId] : []);

  const run = useCallback(
    (body: AiActionRequest, originalText: string) => {
      unsubscribeRef.current?.();
      create.mutate(
        { path: { id: episodeId }, body },
        {
          onSuccess: (res) => {
            if (!res) return;
            setRunId(res.runId);
            setProposal({
              action: body.action,
              paragraphIds: body.paragraphIds ?? [],
              originalText,
              text: "",
              segments: [],
              done: false,
              stepId: res.stepId,
            });
            unsubscribeRef.current = getSseBridge(queryClient).onAiDelta(res.stepId, (evt) => {
              setProposal((current) => {
                if (!current || current.stepId !== evt.stepId) return current;
                const text = current.text + evt.text;
                return { ...current, text, segments: diffText(current.originalText, text), done: evt.done };
              });
            });
          },
        },
      );
    },
    [episodeId, create, queryClient],
  );

  const clear = useCallback(() => {
    unsubscribeRef.current?.();
    unsubscribeRef.current = null;
    setProposal(null);
    setRunId(null);
  }, []);

  return { proposal, run, clear, isStarting: create.isPending, error: create.error as Error | null };
}
