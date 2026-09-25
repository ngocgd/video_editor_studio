import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import { JobProgress } from "./job-progress";

describe("JobProgress", () => {
  it("shows the GPU queue position while waiting instead of the step name", () => {
    render(
      <JobProgress
        name="Episode 3 voice"
        step="align"
        status="queued"
        progress={0}
        gpuQueuePosition={2}
      />,
    );
    expect(screen.getByText("Waiting for GPU slot (#2)")).toBeInTheDocument();
  });

  it("renders Retry and View log actions for a failed step", () => {
    const onRetry = vi.fn();
    const onViewLog = vi.fn();
    render(
      <JobProgress
        name="Episode 3 render"
        step="failed at encode"
        status="failed"
        progress={62}
        onRetry={onRetry}
        onViewLog={onViewLog}
      />,
    );
    expect(screen.getByRole("button", { name: "Retry step" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View log" })).toBeInTheDocument();
  });

  it("has no obvious accessibility violations", async () => {
    const { container } = render(
      <JobProgress name="Episode 1 voice" step="Generating audio" status="running" progress={43} etaS={134} />,
    );
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
