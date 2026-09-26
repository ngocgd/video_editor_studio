import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { getDraftOptions, getDraftQueryKey } from "../../api/gen/@tanstack/react-query.gen";
import { getDraft, patchDraft } from "../../api/gen/sdk.gen";
import type { EpisodeDraft, TargetLanguage } from "../../api/gen/types.gen";
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

  /** Replaces the whole document (an accepted AI proposal), saves it at once and reloads the editor. */
  const replace = useCallback(
    (next: DiffParagraph[]) => {
      if (!queueRef.current) return;
      queueRef.current.replace(next);
      reseed(next);
    },
    [reseed],
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
    replace,
    status,
    conflict: status === "conflict",
    reloadLatest,
    keepMine,
  };
}
