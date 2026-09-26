import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import {
  getScenePeaksOptions,
  listSceneTakesOptions,
  listSceneTakesQueryKey,
  listScenesOptions,
} from "../../api/gen/@tanstack/react-query.gen";
import { generateMissing, regenerateScene, selectSceneTake, splitScenes, updateScene } from "../../api/gen/sdk.gen";
import type { Scene, SceneFilter, SceneLanguage, ScenePatch, SceneSplitRequest, TakeKind } from "../../api/gen/types.gen";
import { getSseBridge } from "../../api/sse-bridge";
import { useSseTopics } from "../../api/use-sse-topics";

/** The browser URL of an asset or one of its variants (a same-origin redirect to a short-lived object URL). */
export function assetUrl(assetId: string, variant: "original" | `${"webp" | "avif"}-${320 | 640 | 1280}` = "original"): string {
  return `/api/v1/assets/${assetId}/variants/${variant}`;
}

function isScenesQuery(queryKey: readonly unknown[], episodeId: string): boolean {
  const first = queryKey[0] as { _id?: string; path?: { id?: string } } | undefined;
  return first?._id === "listScenes" && first.path?.id === episodeId;
}

/**
 * The storyboard snapshot. It subscribes to the SSE topics of every run
 * with queued or running scene steps and refetches when one of their steps
 * (or a scene.updated event) lands; while anything is in flight it also
 * polls slowly, as a safety net for missed events.
 */
export function useScenes(episodeId: string, lang: SceneLanguage, filter: SceneFilter, q: string) {
  const queryClient = useQueryClient();
  const query = useQuery({
    ...listScenesOptions({ path: { id: episodeId }, query: { lang, filter, q: q || undefined, limit: 1000 } }),
    placeholderData: (previous) => previous,
    refetchInterval: (state) => ((state.state.data?.activeRunIds.length ?? 0) > 0 ? 5000 : false),
  });
  const runIds = query.data?.activeRunIds ?? [];
  useSseTopics(runIds);

  useEffect(() => {
    const bridge = getSseBridge(queryClient);
    const watched = new Set(runIds);
    let timer: ReturnType<typeof setTimeout> | null = null;
    const unsubscribe = bridge.onCompletion((evt) => {
      if (!watched.has(evt.run_id) && evt.type !== "scene.updated") return;
      if (timer) return;
      // Coalesce a burst of step completions into one refetch.
      timer = setTimeout(() => {
        timer = null;
        void queryClient.invalidateQueries({ predicate: (q) => isScenesQuery(q.queryKey, episodeId) });
      }, 250);
    });
    return () => {
      unsubscribe();
      if (timer) clearTimeout(timer);
    };
  }, [queryClient, episodeId, runIds.join(",")]); // eslint-disable-line react-hooks/exhaustive-deps

  return query;
}

function useInvalidateScenes(episodeId: string) {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ predicate: (q) => isScenesQuery(q.queryKey, episodeId) });
}

export function useSplitScenes(episodeId: string) {
  const invalidate = useInvalidateScenes(episodeId);
  return useMutation({
    mutationFn: (body: SceneSplitRequest) => splitScenes({ path: { id: episodeId }, body, throwOnError: true }).then((r) => r.data),
    onSuccess: () => invalidate(),
  });
}

export function useGenerateMissing(episodeId: string) {
  const invalidate = useInvalidateScenes(episodeId);
  return useMutation({
    mutationFn: (lang: SceneLanguage) => generateMissing({ path: { id: episodeId }, body: { lang }, throwOnError: true }).then((r) => r.data),
    onSuccess: () => invalidate(),
  });
}

export function useUpdateScene(episodeId: string) {
  const invalidate = useInvalidateScenes(episodeId);
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: ScenePatch }) => updateScene({ path: { id }, body, throwOnError: true }).then((r) => r.data as Scene),
    onSuccess: () => invalidate(),
  });
}

export function useRegenerate(episodeId: string) {
  const invalidate = useInvalidateScenes(episodeId);
  return useMutation({
    mutationFn: ({ id, kind }: { id: string; kind: TakeKind }) => regenerateScene({ path: { id }, body: { kind }, throwOnError: true }).then((r) => r.data),
    onSuccess: () => invalidate(),
  });
}

export function useTakes(sceneId: string | undefined, version: number | undefined) {
  const queryClient = useQueryClient();
  const options = listSceneTakesOptions({ path: { id: sceneId ?? "" } });
  // A new take bumps the scene's version: refetch the strip then.
  useEffect(() => {
    if (sceneId) void queryClient.invalidateQueries({ queryKey: options.queryKey });
  }, [sceneId, version]); // eslint-disable-line react-hooks/exhaustive-deps
  return useQuery({ ...options, enabled: Boolean(sceneId) });
}

export function useSelectTake(episodeId: string) {
  const queryClient = useQueryClient();
  const invalidate = useInvalidateScenes(episodeId);
  return useMutation({
    mutationFn: ({ sceneId, takeId }: { sceneId: string; takeId: string }) =>
      selectSceneTake({ path: { id: sceneId, takeId }, throwOnError: true }).then((r) => r.data as Scene),
    onSuccess: (_scene, vars) => {
      void queryClient.invalidateQueries({ queryKey: listSceneTakesQueryKey({ path: { id: vars.sceneId } }).slice(0, 1) });
      void invalidate();
    },
  });
}

/** Waveform peaks of one scene's voice take; cached for the session. */
export function useScenePeaks(sceneId: string, enabled: boolean) {
  return useQuery({ ...getScenePeaksOptions({ path: { id: sceneId } }), enabled, staleTime: Infinity });
}
