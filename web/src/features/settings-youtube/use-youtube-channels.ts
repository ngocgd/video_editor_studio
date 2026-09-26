import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { listYouTubeChannelsOptions, listYouTubeChannelsQueryKey } from "../../api/gen/@tanstack/react-query.gen";
import { disconnectYouTubeChannel, startYouTubeConnect, updateYouTubeChannelAudit } from "../../api/gen/sdk.gen";
import type { YouTubeChannelAuditUpdate } from "../../api/gen/types.gen";
import { useMe } from "../auth/use-auth";

export function useYouTubeChannels() {
  return useQuery(listYouTubeChannelsOptions());
}

/** Connecting, the audit toggle and disconnecting are owner-only on the server; the UI mirrors that. */
export function useIsTenantOwner(): boolean {
  const { data: me } = useMe();
  return me?.tenants.find((t) => t.tenantId === me.activeTenantId)?.role === "owner";
}

function problemDetail(error: unknown): string {
  if (typeof error === "object" && error !== null && "detail" in error && typeof error.detail === "string") {
    return error.detail;
  }
  return error instanceof Error ? error.message : "Please try again.";
}

function reportError(action: string, error: unknown): void {
  void import("sonner").then(({ toast }) => toast.error(`${action} failed`, { description: problemDetail(error) }));
}

/**
 * Starts the Google consent flow: the server mints a single-use state bound to
 * this session and returns the consent URL, which the browser then follows.
 * Google redirects back to the API callback, which lands on this page again
 * with ?connect=ok|error.
 */
export function useStartYouTubeConnect(navigate: (url: string) => void = (url) => window.location.assign(url)) {
  return useMutation({
    mutationFn: async () => {
      const { data } = await startYouTubeConnect({ throwOnError: true });
      return data;
    },
    onSuccess: (data) => navigate(data.authorizationUrl),
    onError: (error) => reportError("Connect", error),
  });
}

export function useUpdateYouTubeChannelAudit() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: YouTubeChannelAuditUpdate }) => {
      await updateYouTubeChannelAudit({ path: { id }, body, throwOnError: true });
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: listYouTubeChannelsQueryKey() }),
    onError: (error) => reportError("Saving the audit status", error),
  });
}

export function useDisconnectYouTubeChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await disconnectYouTubeChannel({ path: { id }, throwOnError: true });
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: listYouTubeChannelsQueryKey() }),
    onError: (error) => reportError("Disconnect", error),
  });
}
