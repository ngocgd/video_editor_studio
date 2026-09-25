import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  commitImportMutation,
  createImportMutation,
  getImportOptions,
  getImportQueryKey,
  listImportsOptions,
  previewImportMutation,
} from "../../api/gen/@tanstack/react-query.gen";

export function useImportsList() {
  return useQuery(listImportsOptions({ query: { limit: 50 } }));
}

export function useImport(importId: string | null) {
  return useQuery({ ...getImportOptions({ path: { id: importId ?? "" } }), enabled: !!importId });
}

export function useCreateImport() {
  return useMutation(createImportMutation());
}

export function usePreviewImport(importId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    ...previewImportMutation(),
    onSuccess: (data) => {
      if (data) queryClient.setQueryData(getImportQueryKey({ path: { id: importId } }), data);
    },
  });
}

export function useCommitImport() {
  return useMutation(commitImportMutation());
}
