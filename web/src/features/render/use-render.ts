import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import {
  getRenderStatusOptions,
  getRenderStatusQueryKey,
  listRendersOptions,
  listRendersQueryKey,
  listRunStepsQueryKey,
  putRenderSettingsMutation,
  startRenderMutation,
} from "../../api/gen/@tanstack/react-query.gen";
import { listRunSteps } from "../../api/gen/sdk.gen";
import type { PipelineStep, PipelineStepList, SceneLanguage } from "../../api/gen/types.gen";
import { useSseTopics } from "../../api/use-sse-topics";
import { runFinished } from "./render-model";

const STEP_PAGE = 200;
/** A 60-minute episode has about a thousand render steps; stop well above that. */
const MAX_STEP_PAGES = 25;

/** Stage summary, readiness reasons, settings, estimate and the active run of one episode language. */
export function useRenderStatus(episodeId: string, lang: SceneLanguage) {
  return useQuery({
    ...getRenderStatusOptions({ path: { id: episodeId }, query: { lang } }),
    // The disk watermark and the worker's encoder probe change without an
    // event; a slow poll keeps the disabled reasons honest.
    refetchInterval: 30_000,
  });
}

export function useRenders(episodeId: string, lang: SceneLanguage, limit = 10) {
  return useQuery(listRendersOptions({ path: { id: episodeId }, query: { lang, limit } }));
}

function refreshRender(queryClient: ReturnType<typeof useQueryClient>, episodeId: string, lang: SceneLanguage) {
  void queryClient.invalidateQueries({ queryKey: getRenderStatusQueryKey({ path: { id: episodeId }, query: { lang } }) });
  void queryClient.invalidateQueries({ queryKey: listRendersQueryKey({ path: { id: episodeId }, query: { lang, limit: 10 } }) });
}

export function useSaveRenderSettings(episodeId: string, lang: SceneLanguage) {
  const queryClient = useQueryClient();
  return useMutation({ ...putRenderSettingsMutation(), onSuccess: () => refreshRender(queryClient, episodeId, lang) });
}

export function useStartRender(episodeId: string, lang: SceneLanguage) {
  const queryClient = useQueryClient();
  return useMutation({
    ...startRenderMutation(),
    // A refusal (422 not ready, 409 already running, 507 disk) also changes
    // what the status should say, so refresh on both outcomes.
    onSettled: () => refreshRender(queryClient, episodeId, lang),
  });
}

/**
 * Every step of the active render run under the listRunSteps cache key, so
 * the SSE bridge patches progress in place (no page refetch). The list is
 * read page by page once the stream is ready (subscribe before snapshot).
 * When the last step ends, the status and the render list are refreshed.
 */
export function useRenderRun(episodeId: string, lang: SceneLanguage, runId: string | undefined) {
  const queryClient = useQueryClient();
  const { ready } = useSseTopics(runId ? [runId] : []);
  const query = useQuery({
    queryKey: listRunStepsQueryKey({ path: { id: runId ?? "" }, query: { limit: STEP_PAGE } }),
    queryFn: async ({ signal }): Promise<PipelineStepList> => {
      const items: PipelineStep[] = [];
      let cursor: string | undefined;
      for (let page = 0; page < MAX_STEP_PAGES; page++) {
        const { data } = await listRunSteps({ path: { id: runId ?? "" }, query: { limit: STEP_PAGE, cursor }, signal, throwOnError: true });
        items.push(...data.items);
        cursor = data.nextCursor;
        if (!cursor) break;
      }
      return { items };
    },
    enabled: Boolean(runId) && ready,
    // Safety net for anything a degraded stream misses.
    refetchInterval: 20_000,
  });
  const steps = query.data?.items;
  const finished = steps ? runFinished(steps) : false;
  useEffect(() => {
    if (finished) refreshRender(queryClient, episodeId, lang);
  }, [finished, queryClient, episodeId, lang]);
  return { ...query, steps };
}
