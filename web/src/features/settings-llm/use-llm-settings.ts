import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  getClaudeCliStatusOptions,
  getLlmSettingsOptions,
  getLlmSettingsQueryKey,
  putLlmApiKeyMutation,
  putLlmSettingsMutation,
  testLlmSettingsMutation,
} from "../../api/gen/@tanstack/react-query.gen";

export function useLlmSettings() {
  return useQuery(getLlmSettingsOptions());
}

export function useClaudeCliStatus() {
  return useQuery(getClaudeCliStatusOptions());
}

export function useUpdateLlmSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    ...putLlmSettingsMutation(),
    onSuccess: (data) => {
      if (data) queryClient.setQueryData(getLlmSettingsQueryKey(), data);
    },
  });
}

export function useTestLlmProvider() {
  return useMutation(testLlmSettingsMutation());
}

/** PUT /settings/llm/keys/{provider}: write-only, submit-only masked API key entry. */
export function usePutLlmApiKey() {
  return useMutation(putLlmApiKeyMutation());
}
