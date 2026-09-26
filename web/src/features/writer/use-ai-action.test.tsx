import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { client } from "../../api/gen/client.gen";
import { useAiAction } from "./use-ai-action";

const EPISODE = "0190a000-0000-7000-8000-000000000001";
const STEP = "0190a000-0000-7000-8000-000000000002";
const RUN = "0190a000-0000-7000-8000-000000000003";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** Serves createAiAction, then answers each result poll from `results` in order (the last one repeats). */
function stubApi(results: unknown[]) {
  const polls: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (url.endsWith(`/episodes/${EPISODE}/ai-actions`)) return json({ runId: RUN, stepId: STEP }, 202);
    if (url.endsWith(`/episodes/${EPISODE}/ai-actions/${STEP}`)) {
      polls.push(url);
      return json(results[Math.min(polls.length - 1, results.length - 1)]);
    }
    return json({ title: "unexpected" }, 500);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { polls };
}

function wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("useAiAction", () => {
  // Node cannot build a Request from the app's relative base URL.
  beforeEach(() => client.setConfig({ baseUrl: "http://localhost/api/v1" }));
  afterEach(() => {
    vi.unstubAllGlobals();
    client.setConfig({ baseUrl: "/api/v1" });
  });

  it("polls the step until done and then exposes an acceptable diff proposal", async () => {
    const { polls } = stubApi([{ status: "queued" }, { status: "running" }, { status: "done", text: "new text", provider: "ollama", lang: "en", tainted: false }]);
    const { result } = renderHook(() => useAiAction(EPISODE), { wrapper });

    act(() => result.current.run({ action: "rewrite", lang: "en", paragraphIds: ["p_1"] }, "old text"));

    await waitFor(() => expect(result.current.proposal?.stepId).toBe(STEP));
    expect(result.current.proposal?.done).toBe(false);

    await waitFor(() => expect(result.current.proposal?.done).toBe(true), { timeout: 3000 });
    expect(result.current.proposal).toMatchObject({ text: "new text", provider: "ollama", paragraphIds: ["p_1"] });
    expect(result.current.proposal?.segments.length).toBeGreaterThan(0);
    expect(polls.length).toBe(3);

    // A terminal status stops polling.
    await new Promise((resolve) => setTimeout(resolve, 900));
    expect(polls.length).toBe(3);
  });

  it("surfaces a failed step as an error proposal that cannot be accepted", async () => {
    stubApi([{ status: "failed", errorDetail: "provider unavailable" }]);
    const { result } = renderHook(() => useAiAction(EPISODE), { wrapper });

    act(() => result.current.run({ action: "shorten", lang: "en", paragraphIds: ["p_1"] }, "old text"));

    await waitFor(() => expect(result.current.proposal?.error).toBe("provider unavailable"));
    expect(result.current.proposal?.done).toBe(false);

    act(() => result.current.clear());
    expect(result.current.proposal).toBeNull();
  });
});
