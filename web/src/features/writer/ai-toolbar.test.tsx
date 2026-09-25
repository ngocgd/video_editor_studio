import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AiToolbar } from "./ai-toolbar";
import type { AiProposal } from "./use-ai-action";

const baseProposal: AiProposal = {
  action: "rewrite",
  paragraphIds: ["p_1"],
  originalText: "old text",
  text: "new text",
  segments: [
    { op: "remove", text: "old" },
    { op: "insert", text: "new" },
    { op: "keep", text: " text" },
  ],
  done: true,
  stepId: "step-1",
};

describe("AiToolbar", () => {
  it("shows the selection actions when there is no proposal yet", () => {
    render(<AiToolbar disabled={false} onAction={vi.fn()} proposal={null} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    expect(screen.getByRole("button", { name: /rewrite/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /continue/i })).toBeInTheDocument();
  });

  it("calls onAction with the chosen action and trimmed instruction", () => {
    const onAction = vi.fn();
    render(<AiToolbar disabled={false} onAction={onAction} proposal={null} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText(/instruction/i), { target: { value: "make it tenser " } });
    fireEvent.click(screen.getByRole("button", { name: /^expand$/i }));
    expect(onAction).toHaveBeenCalledWith("expand", "make it tenser");
  });

  it("renders the diff proposal and wires Accept/Reject once streaming has a proposal", () => {
    const onAccept = vi.fn();
    const onReject = vi.fn();
    render(<AiToolbar disabled={false} onAction={vi.fn()} proposal={baseProposal} onAccept={onAccept} onReject={onReject} onRetry={vi.fn()} />);

    expect(screen.getByText("old")).toBeInTheDocument();
    expect(screen.getByText("new")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^accept/i }));
    expect(onAccept).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: /^reject/i }));
    expect(onReject).toHaveBeenCalledOnce();
  });

  it("shows a streaming cost label while the proposal is not yet done", () => {
    render(<AiToolbar disabled={false} onAction={vi.fn()} proposal={{ ...baseProposal, done: false }} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    expect(screen.getByText(/streaming/i)).toBeInTheDocument();
  });
});
