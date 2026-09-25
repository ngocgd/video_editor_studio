import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";

import { getMeOptions } from "../../api/gen/@tanstack/react-query.gen";
import { login, logout, switchTenant } from "../../api/gen/sdk.gen";
import type { LoginRequest, Me } from "../../api/gen/types.gen";
import { setCsrfToken } from "../../api/client";

/** The current session; `null` once loaded means "not authenticated". */
export function useMe() {
  return useQuery({ ...getMeOptions(), retry: false, staleTime: 60_000 });
}

export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: LoginRequest) => {
      const result = await login({ body, throwOnError: true });
      return result.data;
    },
    onSuccess: (data) => {
      setCsrfToken(data.csrfToken);
      queryClient.setQueryData<Me>(getMeOptions().queryKey, data.me);
    },
  });
}

export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: async () => {
      await logout({ throwOnError: true });
    },
    onSettled: () => {
      setCsrfToken(null);
      queryClient.clear();
      void navigate({ to: "/login" });
    },
  });
}

export function useSwitchTenant() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (tenantId: string) => {
      const result = await switchTenant({ body: { tenantId }, throwOnError: true });
      return result.data;
    },
    onSuccess: (data) => {
      setCsrfToken(data.csrfToken);
      queryClient.setQueryData<Me>(getMeOptions().queryKey, data.me);
      void queryClient.invalidateQueries();
    },
  });
}
