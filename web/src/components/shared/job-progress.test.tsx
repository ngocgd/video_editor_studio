import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import { JobProgress } from "./job-progress";

describe("JobProgress", () => {
  it("shows the GPU queue position while waiting instead of the step name", () => {
    render(<JobProgress kind="align_voice" status="queued" progress={0} gpuQueuePosition={2} />);
    expect(screen.getByText("Waiting for GPU slot (#2)")).toBeInTheDocument();
  });

  it("humanizes the raw kind into a sentence-case label and keeps the raw kind as a title", () => {
    render(<JobProgress kind="render_episode" status="running" progress={10} />);
    const label = screen.getByText("Render episode");
    expect(label).toHaveAttribute("title", "render_episode");
  });

  it("renders Retry and View log actions for a failed step", () => {
    const onRetry = vi.fn();
    const onViewLog = vi.fn();
    render(<JobProgress kind="render_episode" status="failed" progress={62} onRetry={onRetry} onViewLog={onViewLog} />);
    expect(screen.getByRole("button", { name: "Retry step" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View log" })).toBeInTheDocument();
  });

  it("asks for confirmation before cancelling a running job", () => {
    const onCancel = vi.fn();
    render(<JobProgress kind="render_episode" status="running" progress={43} onCancel={onCancel} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("has no obvious accessibility violations", async () => {
    const { container } = render(<JobProgress kind="align_voice" status="running" progress={43} etaS={134} />);
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
