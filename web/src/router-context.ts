import type { QueryClient } from "@tanstack/react-query";

/** Shared with every route's `beforeLoad`/`loader` so auth/data bootstrapping goes through the query cache (review H6) instead of a bare fetch. */
export interface RouterContext {
  queryClient: QueryClient;
}
