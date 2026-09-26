import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { listModelsOptions, listModelsQueryKey } from "../../api/gen/@tanstack/react-query.gen";
import { installModel, loadModel, pauseModelInstall, unloadModels } from "../../api/gen/sdk.gen";
import type { ModelList } from "../../api/gen/types.gen";
import { useMe } from "../auth/use-auth";

/** Polls fast while a download is running (progress bar), slowly otherwise. */
export function modelsRefetchInterval(data: ModelList | undefined): number | false {
  if (typeof document !== "undefined" && document.hidden) return false;
  return data?.items.some((m) => m.status === "downloading") ? 2000 : 10_000;
}

export function useModels() {
  return useQuery({
    ...listModelsOptions(),
    refetchInterval: (query) => modelsRefetchInterval(query.state.data),
  });
}

export type ModelRole = "owner" | "editor" | "viewer";

/** The caller's role in the active tenant; install/pause need owner, load/unload editor. */
export function useModelRole(): ModelRole {
  const { data: me } = useMe();
  const role = me?.tenants.find((t) => t.tenantId === me.activeTenantId)?.role;
  return role === "owner" || role === "editor" ? role : "viewer";
}

function reportError(action: string, error: unknown): void {
  const detail =
    typeof error === "object" && error !== null && "detail" in error && typeof error.detail === "string"
      ? error.detail
      : error instanceof Error
        ? error.message
        : "Please try again.";
  void import("sonner").then(({ toast }) => toast.error(`${action} failed`, { description: detail }));
}

type Action = "install" | "pause" | "load" | "unload";

const ACTION_LABEL: Record<Action, string> = { install: "Install", pause: "Pause", load: "Load", unload: "Unload" };

/** One mutation for every row action; the list is refetched after each. */
export function useModelAction() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({ action, name }: { action: Action; name: string }) => {
      const path = { name };
      switch (action) {
        case "install":
          await installModel({ path, throwOnError: true });
          break;
        case "pause":
          await pauseModelInstall({ path, throwOnError: true });
          break;
        case "load":
          await loadModel({ path, throwOnError: true });
          break;
        case "unload":
          await unloadModels({ throwOnError: true });
          break;
      }
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: listModelsQueryKey() }),
    onError: (error, variables) => reportError(ACTION_LABEL[variables.action], error),
  });
}
