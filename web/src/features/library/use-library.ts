import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  confirmLibraryCleanupMutation,
  getLibrarySettingsOptions,
  getLibrarySettingsQueryKey,
  getLibraryUsageOptions,
  listLibraryAssetsInfiniteOptions,
  previewLibraryCleanupMutation,
  putLibrarySettingsMutation,
} from "../../api/gen/@tanstack/react-query.gen";
import type { AssetKind } from "../../api/gen/types.gen";

const PAGE = 100;

/** Cursor-paged asset list; the table virtualizes whatever pages are loaded. */
export function useLibraryAssets(kind: AssetKind | undefined, seriesId: string | undefined) {
  return useInfiniteQuery({
    ...listLibraryAssetsInfiniteOptions({ query: { kind, seriesId, limit: PAGE } }),
    initialPageParam: {},
    getNextPageParam: (last) => last.nextCursor,
  });
}

export function useLibraryUsage() {
  return useQuery({ ...getLibraryUsageOptions(), refetchInterval: 60_000 });
}

export function useLibrarySettings() {
  return useQuery(getLibrarySettingsOptions());
}

export function useSaveLibrarySettings() {
  const queryClient = useQueryClient();
  return useMutation({
    ...putLibrarySettingsMutation(),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: getLibrarySettingsQueryKey() }),
  });
}

export function usePreviewCleanup() {
  return useMutation(previewLibraryCleanupMutation());
}

/** Confirms a previewed cleanup; the list and usage refresh once it is queued (the step deletes shortly after). */
export function useConfirmCleanup() {
  const queryClient = useQueryClient();
  return useMutation({
    ...confirmLibraryCleanupMutation(),
    onSuccess: () => {
      for (const id of ["listLibraryAssets", "getLibraryUsage", "getLibrarySettings"]) {
        void queryClient.invalidateQueries({ predicate: (q) => (q.queryKey[0] as { _id?: string } | undefined)?._id === id });
      }
    },
  });
}
