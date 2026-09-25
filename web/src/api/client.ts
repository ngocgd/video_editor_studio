import { QueryClient } from "@tanstack/react-query";

import { client } from "./gen/client.gen";
import type { Problem } from "./gen/types.gen";

/** Methods that mutate state and therefore need the CSRF header. */
const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/** Auth endpoints must never trigger the 401 -> /login redirect themselves. */
const AUTH_PATHS = ["/auth/login", "/auth/logout", "/auth/csrf", "/auth/me"];

let csrfToken: string | null = null;

/** Stores the CSRF token issued by login or GET /auth/csrf (in memory only; never localStorage). */
export function setCsrfToken(token: string | null): void {
  csrfToken = token;
}

export function getCsrfToken(): string | null {
  return csrfToken;
}

/** Typed error thrown by the API client for both network and problem+json failures. */
export class ApiError extends Error {
  readonly status: number;
  readonly title: string;
  readonly type?: string;
  readonly detail?: string;
  readonly instance?: string;

  constructor(problem: Partial<Problem> & { title: string; status: number }) {
    super(problem.detail ?? problem.title);
    this.name = "ApiError";
    this.status = problem.status;
    this.title = problem.title;
    this.type = problem.type;
    this.detail = problem.detail;
    this.instance = problem.instance;
  }

  get isAuthError(): boolean {
    return this.status === 401;
  }
}

function isProblem(value: unknown): value is Problem {
  return (
    typeof value === "object" &&
    value !== null &&
    "title" in value &&
    "status" in value &&
    typeof (value as Problem).status === "number"
  );
}

client.setConfig({ baseUrl: `${window.location.origin}/api/v1` });

client.interceptors.request.use((request, options) => {
  if (UNSAFE_METHODS.has(request.method) && csrfToken) {
    request.headers.set("X-CSRF-Token", csrfToken);
  }
  void options;
  return request;
});

client.interceptors.response.use((response) => {
  const path = new URL(response.url).pathname.replace(/^\/api\/v1/, "");
  if (response.status === 401 && !AUTH_PATHS.includes(path) && window.location.pathname !== "/login") {
    const redirect = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.assign(`/login?redirect=${redirect}`);
  }
  return response;
});

client.interceptors.error.use((error) => {
  if (isProblem(error)) {
    return new ApiError(error);
  }
  if (error instanceof Error) {
    return new ApiError({ title: "Network error", status: 0, detail: error.message });
  }
  return new ApiError({ title: "Unknown error", status: 0, detail: typeof error === "string" ? error : undefined });
});

/**
 * Query defaults shared by every screen: a 30s stale time keeps SSE-backed
 * entities from refetching on focus (the bridge patches the cache instead),
 * GETs retry once for transient failures, mutations never auto-retry.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
    mutations: {
      retry: 0,
    },
  },
});

export { client };
