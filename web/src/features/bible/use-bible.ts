import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { getBibleOptions, getBibleQueryKey } from "../../api/gen/@tanstack/react-query.gen";
import { updateBibleSection } from "../../api/gen/sdk.gen";
import type { BibleSectionUpdateRequest, StoryBible } from "../../api/gen/types.gen";
import { ApiError } from "../../api/client";

export function useBible(seriesId: string) {
  return useQuery(getBibleOptions({ path: { id: seriesId } }));
}

/**
 * Optimistic-concurrency PATCH for one bible section (phase 6). On a 409
 * (stale expectedVersion) the caller must refetch and offer the merge
 * prompt; this hook only classifies the error so the view can decide.
 */
export function useUpdateBibleSection(seriesId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: BibleSectionUpdateRequest) =>
      updateBibleSection({ path: { id: seriesId }, body, throwOnError: true }).then((r) => r.data as StoryBible),
    onSuccess: (data) => {
      queryClient.setQueryData(getBibleQueryKey({ path: { id: seriesId } }), data);
    },
  });
}

export function isVersionConflict(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409;
}
