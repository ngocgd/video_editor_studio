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
  it("keeps selection-only actions off with just a caret but allows Continue and Translate", () => {
    const onAction = vi.fn();
    render(<AiToolbar disabled={false} hasSelection={false} onAction={onAction} proposal={null} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    expect(screen.getByRole("button", { name: /rewrite/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /tenser/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    expect(onAction).toHaveBeenCalledWith("continue", undefined);
    expect(screen.getByRole("button", { name: /translate/i })).toBeEnabled();
  });

  it("shows the selection actions when there is no proposal yet", () => {
    render(<AiToolbar disabled={false} hasSelection onAction={vi.fn()} proposal={null} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    expect(screen.getByRole("button", { name: /rewrite/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /continue/i })).toBeInTheDocument();
  });

  it("calls onAction with the chosen action and trimmed instruction", () => {
    const onAction = vi.fn();
    render(<AiToolbar disabled={false} hasSelection onAction={onAction} proposal={null} onAccept={vi.fn()} onReject={vi.fn()} onRetry={vi.fn()} />);
    fireEvent.change(screen.getByPlaceholderText(/instruction/i), { target: { value: "make it tenser " } });
    fireEvent.click(screen.getByRole("button", { name: /^expand$/i }));
    expect(onAction).toHaveBeenCalledWith("expand", "make it tenser");
  });

  it("renders the diff proposal and wires Accept/Reject once streaming has a proposal", () => {
    const onAccept = vi.fn();
    const onReject = vi.fn();
    render(<AiToolbar disabled={false} hasSelection onAction={vi.fn()} proposal={baseProposal} onAccept={onAccept} onReject={onReject} onRetry={vi.fn()} />);

    expect(screen.getByText("old")).toBeInTheDocument();
    expect(screen.getByText("new")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^accept/i }));
    expect(onAccept).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: /^reject/i }));
    expect(onReject).toHaveBeenCalledOnce();
  });

  it("shows a generating state without Accept while the proposal is not yet done", () => {
    const onReject = vi.fn();
    render(<AiToolbar disabled={false} hasSelection onAction={vi.fn()} proposal={{ ...baseProposal, text: "", segments: [], done: false }} onAccept={vi.fn()} onReject={onReject} onRetry={vi.fn()} />);
    expect(screen.getByRole("status")).toHaveTextContent(/generating/i);
    expect(screen.queryByRole("button", { name: /^accept/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^dismiss/i }));
    expect(onReject).toHaveBeenCalledOnce();
  });

  it("shows the step error with Retry instead of a diff when the action failed", () => {
    const onRetry = vi.fn();
    render(
      <AiToolbar disabled={false} hasSelection onAction={vi.fn()} proposal={{ ...baseProposal, done: false, error: "provider unavailable" }} onAccept={vi.fn()} onReject={vi.fn()} onRetry={onRetry} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("provider unavailable");
    expect(screen.queryByRole("button", { name: /^accept/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^retry/i }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
