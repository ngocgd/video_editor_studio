import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { getDraftOptions, getDraftQueryKey } from "../../api/gen/@tanstack/react-query.gen";
import { ApiError } from "../../api/client";
import { applyDraftStep, createDraft, getDraft, patchDraft } from "../../api/gen/sdk.gen";
import type { ApplyDraftStepRequest, EpisodeDraft, TargetLanguage } from "../../api/gen/types.gen";
import { DraftAutosaveQueue, type SaveStatus } from "./draft-autosave-queue";
import type { DiffParagraph } from "./paragraph-diff";

/** What the editor mounts with; a new `key` means the editor must remount on `paragraphs`. */
export interface DraftSeed {
  key: number;
  paragraphs: DiffParagraph[];
}

function toDiffParagraphs(draft: EpisodeDraft): DiffParagraph[] {
  return draft.paragraphs.map((p) => ({ id: p.id, text: p.text }));
}

/**
 * Owns one episode draft's load and autosave (see ./draft-autosave-queue).
 *
 * The editor is seeded only from a settled fetch, and the autosave base is
 * set from that same data, so a later background refetch can never move the
 * base under an editor still showing older content. A refetch that brings a
 * version newer than the base (someone else saved) is reported as a
 * conflict instead. Switching language or leaving the page flushes pending
 * edits for the draft they were typed in.
 */
export function useDraft(episodeId: string, lang: TargetLanguage) {
  const queryClient = useQueryClient();
  const draftQuery = useQuery(getDraftOptions({ path: { id: episodeId, lang } }));

  const queueRef = useRef<DraftAutosaveQueue | null>(null);
  const [seed, setSeed] = useState<DraftSeed | null>(null);
  const [status, setStatus] = useState<SaveStatus>("saved");

  // A new episode or language starts from nothing; the previous queue saves
  // what it still holds for its own draft before it is dropped.
  useEffect(() => {
    setSeed(null);
    setStatus("saved");
    return () => {
      queueRef.current?.flush();
      queueRef.current?.stop();
      queueRef.current = null;
    };
  }, [episodeId, lang]);

  const reseed = useCallback((paragraphs: DiffParagraph[]) => {
    setSeed((prev) => ({ key: (prev?.key ?? 0) + 1, paragraphs }));
  }, []);

  const data = draftQuery.data;
  const settled = !draftQuery.isFetching;
  useEffect(() => {
    if (!data || !settled || data.episodeId !== episodeId || data.lang !== lang) return;
    const queue = queueRef.current;
    if (!queue) {
      const queryKey = getDraftQueryKey({ path: { id: episodeId, lang } });
      queueRef.current = new DraftAutosaveQueue(data, {
        send: async (expectedVersion, ops) =>
          (await patchDraft({ path: { id: episodeId, lang }, body: { expectedVersion, ops }, throwOnError: true })).data,
        onSaved: (draft) => queryClient.setQueryData(queryKey, draft),
        onStatus: setStatus,
      });
      reseed(toDiffParagraphs(data));
    } else if (data.version > queue.version) {
      setStatus("conflict");
    }
  }, [data, settled, episodeId, lang, queryClient, reseed]);

  // Warn before leaving with edits the server has not stored.
  useEffect(() => {
    if (status === "saved") return;
    const handler = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [status]);

  const fetchLatest = useCallback(async () => {
    const latest = (await getDraft({ path: { id: episodeId, lang }, throwOnError: true })).data;
    queryClient.setQueryData(getDraftQueryKey({ path: { id: episodeId, lang } }), latest);
    return latest;
  }, [episodeId, lang, queryClient]);

  /** Debounced autosave of the editor's own edits. */
  const edit = useCallback((current: DiffParagraph[]) => queueRef.current?.edit(current), []);

  /** Creates this language's (empty) draft; the editor mounts once it arrives. */
  const create = useCallback(async () => {
    const draft = (await createDraft({ path: { id: episodeId, lang }, throwOnError: true })).data;
    queryClient.setQueryData(getDraftQueryKey({ path: { id: episodeId, lang } }), draft);
  }, [episodeId, lang, queryClient]);

  /**
   * Applies a finished AI action step on the server, which stores its text
   * with the step's provenance and taint. Pending edits are saved first so
   * the server applies the step to what the editor shows; the editor then
   * reloads from the returned draft. `draftLang` is the draft the step
   * writes into (a translation goes to the other language's draft).
   * Resolves to an error message, or null on success.
   */
  const applyStep = useCallback(
    async (body: ApplyDraftStepRequest, draftLang: TargetLanguage): Promise<string | null> => {
      const queue = queueRef.current;
      if (queue && !(await queue.settle())) return "Your latest edits are not saved yet, so the AI result was not applied. Try again once the draft shows Saved.";
      try {
        const draft = (await applyDraftStep({ path: { id: episodeId, lang: draftLang }, body, throwOnError: true })).data;
        queryClient.setQueryData(getDraftQueryKey({ path: { id: episodeId, lang: draftLang } }), draft);
        if (draftLang === lang && queueRef.current) {
          queueRef.current.reset(draft);
          reseed(toDiffParagraphs(draft));
        }
        return null;
      } catch (error) {
        return error instanceof ApiError ? error.message : "Could not apply the AI result.";
      }
    },
    [episodeId, lang, queryClient, reseed],
  );

  /** The editor's latest paragraphs, including edits not yet saved. */
  const current = useCallback(() => queueRef.current?.current ?? [], []);

  /** Resolves a conflict by discarding local edits and loading the latest version. */
  const reloadLatest = useCallback(async () => {
    const latest = await fetchLatest();
    queueRef.current?.reset(latest);
    reseed(toDiffParagraphs(latest));
  }, [fetchLatest, reseed]);

  /** Resolves a conflict by re-applying this editor's changes on top of the latest version. */
  const keepMine = useCallback(async () => {
    const queue = queueRef.current;
    if (!queue) return;
    const merged = await queue.mergeOnto(await fetchLatest());
    if (merged) reseed(toDiffParagraphs(merged));
  }, [fetchLatest, reseed]);

  return {
    /** Null until the draft has loaded (and while it does not exist). */
    seed,
    exists: draftQuery.data !== undefined,
    isLoading: draftQuery.isLoading || (draftQuery.data !== undefined && seed === null),
    current,
    edit,
    create,
    applyStep,
    status,
    conflict: status === "conflict",
    reloadLatest,
    keepMine,
  };
}
