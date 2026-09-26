import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getAnalyticsExplanationOptions,
  getAnalyticsOverviewOptions,
  getAnalyticsVideoOptions,
  listAnalyticsSuggestionsOptions,
} from "../../api/gen/@tanstack/react-query.gen";
import {
  explainAnalyticsChannel,
  listAnalyticsVideos,
  syncAnalyticsChannel,
  trackAnalyticsVideo,
  untrackAnalyticsVideo,
  updateAnalyticsSuggestion,
} from "../../api/gen/sdk.gen";
import type { AnalyticsSuggestionUpdate, ListAnalyticsVideosData } from "../../api/gen/types.gen";
import { useMe } from "../auth/use-auth";

export type VideoSort = NonNullable<ListAnalyticsVideosData["query"]["sort"]>;
export type SortOrder = NonNullable<ListAnalyticsVideosData["query"]["order"]>;

const VIDEO_PAGE_SIZE = 100;
const EXPLANATION_POLL_MS = 2000;

/** Sync, tracking, dismissing and Explain need the editor role on the server; the UI mirrors that. */
export function useCanEditAnalytics(): boolean {
  const { data: me } = useMe();
  const role = me?.tenants.find((t) => t.tenantId === me.activeTenantId)?.role;
  return role === "owner" || role === "editor";
}

export function problemDetail(error: unknown): string {
  if (typeof error === "object" && error !== null && "detail" in error && typeof error.detail === "string") {
    return error.detail;
  }
  return error instanceof Error ? error.message : "Please try again.";
}

function reportError(action: string, error: unknown): void {
  void import("sonner").then(({ toast }) => toast.error(`${action} failed`, { description: problemDetail(error) }));
}

function reportSuccess(message: string): void {
  void import("sonner").then(({ toast }) => toast.success(message));
}

/** Every analytics query key starts with one of these operation ids; invalidating them refreshes the page after a change. */
function invalidateAnalytics(queryClient: ReturnType<typeof useQueryClient>): Promise<void> {
  return queryClient.invalidateQueries({
    predicate: (query) => {
      const head = query.queryKey[0];
      const id = typeof head === "object" && head !== null && "_id" in head ? String(head._id) : String(head);
      return id.includes("Analytics");
    },
  });
}

export function useAnalyticsOverview(channelId: string | undefined) {
  return useQuery({ ...getAnalyticsOverviewOptions({ path: { id: channelId ?? "" } }), enabled: !!channelId });
}

export function useAnalyticsVideos(channelId: string | undefined, sort: VideoSort, order: SortOrder) {
  return useInfiniteQuery({
    queryKey: ["listAnalyticsVideos", channelId, sort, order],
    enabled: !!channelId,
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => {
      const { data } = await listAnalyticsVideos({
        query: { channelId: channelId ?? "", sort, order, cursor: pageParam, limit: VIDEO_PAGE_SIZE },
        signal,
        throwOnError: true,
      });
      return data;
    },
    getNextPageParam: (page) => page.nextCursor,
  });
}

export function useAnalyticsVideo(videoId: string) {
  return useQuery(getAnalyticsVideoOptions({ path: { videoId } }));
}

export function useAnalyticsSuggestions(channelId: string | undefined, videoId?: string) {
  return useQuery({
    ...listAnalyticsSuggestionsOptions({ path: { id: channelId ?? "" }, query: videoId ? { videoId } : undefined }),
    enabled: !!channelId,
  });
}

export function useSyncAnalytics() {
  return useMutation({
    mutationFn: async (channelId: string) => {
      await syncAnalyticsChannel({ path: { id: channelId }, throwOnError: true });
    },
    onSuccess: () => reportSuccess("Sync queued. New figures appear once the sync finishes."),
    onError: (error) => reportError("Sync", error),
  });
}

export function useTrackVideo() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ channelId, video }: { channelId: string; video: string }) => {
      const { data } = await trackAnalyticsVideo({ body: { channelId, video }, throwOnError: true });
      return data;
    },
    onSuccess: (data) => reportSuccess(`Tracking "${data.title}". Its figures appear after the next sync.`),
    onSettled: () => void invalidateAnalytics(queryClient),
    onError: (error) => reportError("Tracking the video", error),
  });
}

export function useUntrackVideo() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (videoId: string) => {
      await untrackAnalyticsVideo({ path: { videoId }, throwOnError: true });
    },
    onSettled: () => void invalidateAnalytics(queryClient),
    onError: (error) => reportError("Untracking the video", error),
  });
}

export function useDismissSuggestion(channelId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: AnalyticsSuggestionUpdate) => {
      await updateAnalyticsSuggestion({ path: { id: channelId }, body, throwOnError: true });
    },
    onSettled: () => void invalidateAnalytics(queryClient),
    onError: (error) => reportError("Updating the suggestion", error),
  });
}

export function useExplainChannel() {
  return useMutation({
    mutationFn: async (channelId: string) => {
      const { data } = await explainAnalyticsChannel({ path: { id: channelId }, throwOnError: true });
      return data;
    },
    onError: (error) => reportError("Explain", error),
  });
}

const TERMINAL_EXPLANATION = new Set(["done", "failed", "canceled"]);

/** Polls an explanation step until it reaches a terminal status. */
export function useAnalyticsExplanation(id: string | undefined) {
  return useQuery({
    ...getAnalyticsExplanationOptions({ path: { id: id ?? "" } }),
    enabled: !!id,
    refetchInterval: (query) => (TERMINAL_EXPLANATION.has(query.state.data?.status ?? "") ? false : EXPLANATION_POLL_MS),
  });
}
