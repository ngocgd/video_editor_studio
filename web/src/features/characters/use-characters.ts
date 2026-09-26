import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { listCharactersOptions, listCharactersQueryKey, listRunStepsOptions } from "../../api/gen/@tanstack/react-query.gen";
import {
  addCharacterRef,
  createCharacter,
  deleteCharacter,
  deleteCharacterRef,
  previewCharacterVoice,
  regenerateCharacterSheet,
  setCharacterVoice,
  setNarratorVoice,
  trainCharacterLora,
  updateCharacter,
  updateCharacterRef,
} from "../../api/gen/sdk.gen";
import type { CharacterInput, VoiceAssignment, VoiceLanguage } from "../../api/gen/types.gen";

export function useCharacters(seriesId: string) {
  return useQuery(listCharactersOptions({ path: { id: seriesId } }));
}

/** Every character mutation, each refreshing the series' character list. */
export function useCharacterActions(seriesId: string) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: listCharactersQueryKey({ path: { id: seriesId } }) });
  const m = <A, R>(fn: (a: A) => Promise<R>) => useMutation({ mutationFn: fn, onSuccess: () => void refresh() }); // eslint-disable-line react-hooks/rules-of-hooks
  return {
    create: m((body: CharacterInput) => createCharacter({ path: { id: seriesId }, body, throwOnError: true }).then((r) => r.data)),
    update: m(({ id, body }: { id: string; body: CharacterInput }) => updateCharacter({ path: { id }, body, throwOnError: true }).then((r) => r.data)),
    remove: m((id: string) => deleteCharacter({ path: { id }, throwOnError: true })),
    addRef: m(({ id, assetId }: { id: string; assetId: string }) => addCharacterRef({ path: { id }, body: { assetId }, throwOnError: true }).then((r) => r.data)),
    updateRef: m(({ id, refId, approved, angle }: { id: string; refId: string; approved?: boolean; angle?: string }) =>
      updateCharacterRef({ path: { id, refId }, body: { approved, angle }, throwOnError: true }).then((r) => r.data),
    ),
    removeRef: m(({ id, refId }: { id: string; refId: string }) => deleteCharacterRef({ path: { id, refId }, throwOnError: true })),
    sheet: m(({ id, refId }: { id: string; refId: string }) => regenerateCharacterSheet({ path: { id }, body: { refId }, throwOnError: true }).then((r) => r.data)),
    train: m((id: string) => trainCharacterLora({ path: { id }, body: {}, throwOnError: true }).then((r) => r.data)),
    setVoice: m(({ id, lang, body }: { id: string; lang: VoiceLanguage; body: VoiceAssignment }) =>
      setCharacterVoice({ path: { id, lang }, body, throwOnError: true }).then((r) => r.data),
    ),
    setNarrator: m(({ lang, body }: { lang: VoiceLanguage; body: VoiceAssignment }) =>
      setNarratorVoice({ path: { id: seriesId, lang }, body, throwOnError: true }).then((r) => r.data),
    ),
    preview: m(({ id, lang, text }: { id: string; lang: VoiceLanguage; text: string }) =>
      previewCharacterVoice({ path: { id, lang }, body: { text }, throwOnError: true }).then((r) => r.data),
    ),
  };
}

/** Polls a run's steps until they all end; refreshes the characters when they do. */
export function useRunProgress(seriesId: string, runId: string | undefined) {
  const queryClient = useQueryClient();
  return useQuery({
    ...listRunStepsOptions({ path: { id: runId ?? "" } }),
    enabled: Boolean(runId),
    refetchInterval: (q) => {
      const items = q.state.data?.items ?? [];
      const done = items.length > 0 && items.every((s) => ["done", "failed", "canceled"].includes(s.status));
      if (done) void queryClient.invalidateQueries({ queryKey: listCharactersQueryKey({ path: { id: seriesId } }) });
      return done ? false : 2000;
    },
  });
}
