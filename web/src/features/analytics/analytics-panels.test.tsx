import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import type { AnalyticsOverview, AnalyticsSuggestion, AnalyticsVideoRow } from "../../api/gen/types.gen";
import { REACH_PENDING } from "./analytics-format";
import { OverviewPanel } from "./channel-overview";
import { ExplanationResult } from "./explain-panel";
import { SuggestionList } from "./suggestions-panel";
import { videoColumns } from "./video-table";

// uPlot needs a canvas; the chart module is replaced so the panels render in jsdom.
vi.mock("./uplot-render", () => ({
  renderChart: () => ({ setWidth: () => {}, destroy: () => {} }),
}));

function overview(overrides: Partial<AnalyticsOverview> = {}): AnalyticsOverview {
  return {
    sync: { status: "idle", lastError: "", analyticsThrough: "2026-09-24", subscriberCount: 812 },
    window: { from: "2026-08-28", to: "2026-09-24" },
    days: [
      { date: "2026-09-23", views: 120, watchHours: 8.5, subscribersGained: 4, subscribersLost: 1, unavailable: {} },
      { date: "2026-09-24", views: 80, subscribersGained: 1, unavailable: { estimatedMinutesWatched: "gap" } },
    ],
    ypp: { watchHours: 1250.4, watchHoursTarget: 4000, windowFrom: "2025-09-25", windowTo: "2026-09-24", daysMissing: 30, subscribers: 812, subscribersTarget: 1000 },
    ...overrides,
  };
}

describe("OverviewPanel", () => {
  it("shows window totals, data-through stamps and YPP progress without a11y violations", async () => {
    const onSync = vi.fn();
    const { container } = render(<OverviewPanel overview={overview()} canSync syncing={false} onSync={onSync} />);
    expect(screen.getByText("200")).toBeInTheDocument();
    expect(screen.getByText("Sep 24, 2026")).toBeInTheDocument();
    expect(screen.getByText("pending first report")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Watch hours (365 days)" })).toHaveAttribute("aria-valuenow", "1250.4");
    expect(screen.getByText(/30 of the 365 days/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Sync now/ }));
    expect(onSync).toHaveBeenCalledOnce();
    expect(await axe(container)).toHaveNoViolations();
  });

  it("hides Sync now for viewers and disables it while a sync runs", () => {
    const { rerender } = render(<OverviewPanel overview={overview()} canSync={false} syncing={false} />);
    expect(screen.queryByRole("button", { name: /Sync now/ })).toBeNull();
    rerender(<OverviewPanel overview={overview({ sync: { status: "running", lastError: "" } })} canSync syncing={false} />);
    expect(screen.getByRole("button", { name: /Sync now/ })).toBeDisabled();
    expect(screen.getAllByText("Not available from API").length).toBeGreaterThan(0);
  });
});

describe("video table columns", () => {
  const row: AnalyticsVideoRow = { videoId: "dQw4w9WgXcQ", title: "Episode 1", source: "manual", views: 1500, watchHours: 20 };

  function cells(reachThrough?: string) {
    const columns = videoColumns(reachThrough);
    render(
      <div>
        {columns.map((c) => (
          <div key={c.key}>{c.render(row)}</div>
        ))}
      </div>,
    );
  }

  it("shows reach metrics as pending before the first reach report", () => {
    cells();
    expect(screen.getAllByText(REACH_PENDING)).toHaveLength(2);
    expect(screen.getByText("1,500")).toBeInTheDocument();
  });

  it("shows missing reach metrics as not available once reach data exists", () => {
    cells("2026-09-22");
    expect(screen.queryByText(REACH_PENDING)).toBeNull();
    expect(screen.getAllByText("Not available from API").length).toBe(3);
  });
});

describe("SuggestionList", () => {
  const item: AnalyticsSuggestion = {
    videoId: "dQw4w9WgXcQ",
    rule: "low_ctr",
    version: 1,
    title: "Low click-through rate",
    evidence: { ctr: 0.012, medianCtr: 0.041, impressions: 5300 },
    dismissed: false,
    updatedAt: "2026-09-25T02:00:00Z",
  };

  it("lists each suggestion's evidence and dismisses it", () => {
    const onDismiss = vi.fn();
    render(<SuggestionList items={[item]} onDismiss={onDismiss} />);
    expect(screen.getByText("Median ctr")).toBeInTheDocument();
    expect(screen.getByText("5,300")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss suggestion: Low click-through rate" }));
    expect(onDismiss).toHaveBeenCalledWith(item);
  });

  it("has no dismiss button without edit rights", () => {
    render(<SuggestionList items={[item]} />);
    expect(screen.queryByRole("button")).toBeNull();
  });
});

describe("ExplanationResult", () => {
  it("renders model output as plain text with provider and cost", () => {
    const { container } = render(
      <ExplanationResult explanation={{ id: "e1", status: "done", text: "<b>Views rose</b>", provider: "ollama", model: "qwen3.5-9b", costUsd: 0 }} />,
    );
    expect(screen.getByText("<b>Views rose</b>")).toBeInTheDocument();
    expect(container.querySelector("b")).toBeNull();
    expect(screen.getByText(/ollama \/ qwen3.5-9b/)).toBeInTheDocument();
  });

  it("shows progress and failures", () => {
    const { rerender } = render(<ExplanationResult explanation={{ id: "e1", status: "running" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("running");
    rerender(<ExplanationResult explanation={{ id: "e1", status: "failed", error: "quota exceeded" }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("quota exceeded");
  });
});
