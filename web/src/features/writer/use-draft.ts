import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { getDraftOptions, getDraftQueryKey, patchDraftMutation } from "../../api/gen/@tanstack/react-query.gen";
import { ApiError } from "../../api/client";
import type { ParagraphOp, TargetLanguage } from "../../api/gen/types.gen";
import { diffParagraphs, type DiffParagraph } from "./paragraph-diff";

const AUTOSAVE_DEBOUNCE_MS = 1000;

/**
 * Owns one episode draft's load + save lifecycle (phase 6 autosave):
 * debounces paragraph-op diffs 1s after the last edit, PATCHes with
 * `expectedVersion`, and surfaces a 409 as `conflict` for the caller to
 * show the merge prompt. `applyOpsNow` bypasses the debounce for an
 * immediate save (AI-action Accept).
 */
export function useDraft(episodeId: string, lang: TargetLanguage) {
  const queryClient = useQueryClient();
  const draftQuery = useQuery(getDraftOptions({ path: { id: episodeId, lang } }));
  const patch = useMutation(patchDraftMutation());

  const savedRef = useRef<{ paragraphs: DiffParagraph[]; version: number } | null>(null);
  const [conflict, setConflict] = useState(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (draftQuery.data) {
      savedRef.current = {
        paragraphs: draftQuery.data.paragraphs.map((p) => ({ id: p.id, text: p.text })),
        version: draftQuery.data.version,
      };
    }
  }, [draftQuery.data]);

  useEffect(() => () => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
  }, []);

  const applyOps = useCallback(
    (ops: ParagraphOp[]) => {
      const saved = savedRef.current;
      if (!saved || ops.length === 0) return;
      patch.mutate(
        { path: { id: episodeId, lang }, body: { expectedVersion: saved.version, ops } },
        {
          onSuccess: (draft) => {
            if (!draft) return;
            savedRef.current = { paragraphs: draft.paragraphs.map((p) => ({ id: p.id, text: p.text })), version: draft.version };
            queryClient.setQueryData(getDraftQueryKey({ path: { id: episodeId, lang } }), draft);
          },
          onError: (error) => {
            if (error instanceof ApiError && error.status === 409) setConflict(true);
          },
        },
      );
    },
    [episodeId, lang, patch, queryClient],
  );

  /** Debounced autosave from the editor's own edits. */
  const scheduleSave = useCallback(
    (current: DiffParagraph[]) => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        const saved = savedRef.current;
        if (!saved) return;
        applyOps(diffParagraphs(saved.paragraphs, current));
      }, AUTOSAVE_DEBOUNCE_MS);
    },
    [applyOps],
  );

  /** Immediate save for an accepted AI proposal; cancels any pending autosave first. */
  const applyOpsNow = useCallback(
    (ops: ParagraphOp[]) => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
      applyOps(ops);
    },
    [applyOps],
  );

  const reloadAfterConflict = useCallback(() => {
    setConflict(false);
    void draftQuery.refetch();
  }, [draftQuery]);

  return {
    draft: draftQuery.data,
    isLoading: draftQuery.isLoading,
    scheduleSave,
    applyOpsNow,
    conflict,
    dismissConflict: () => setConflict(false),
    reloadAfterConflict,
    saving: patch.isPending,
  };
}
