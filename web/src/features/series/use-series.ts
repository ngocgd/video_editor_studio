import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  createSeriesMutation,
  generateSeriesMutation,
  listSeriesOptions,
  updateSeriesMutation,
} from "../../api/gen/@tanstack/react-query.gen";

export function useSeriesList() {
  return useQuery({ ...listSeriesOptions({ query: { limit: 100 } }) });
}

function matchesId(queryKey: readonly unknown[], id: string): boolean {
  const first = queryKey[0];
  return typeof first === "object" && first !== null && (first as { _id?: string })._id === id;
}

function invalidateSeries(queryClient: ReturnType<typeof useQueryClient>): void {
  void queryClient.invalidateQueries({ predicate: (q) => matchesId(q.queryKey, "listSeries") });
}

export function useCreateSeries() {
  const queryClient = useQueryClient();
  return useMutation({ ...createSeriesMutation(), onSuccess: () => invalidateSeries(queryClient) });
}

export function useUpdateSeries() {
  const queryClient = useQueryClient();
  return useMutation({ ...updateSeriesMutation(), onSuccess: () => invalidateSeries(queryClient) });
}

/** Kicks off the settings -> bible seed -> episode outlines wizard; caller subscribes the returned runId over SSE via JobProgress. */
export function useGenerateSeries() {
  return useMutation({ ...generateSeriesMutation() });
}
