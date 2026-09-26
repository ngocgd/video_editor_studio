import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChapterPreviewTable } from "./chapter-preview-table";

const chapters = [
  { index: 1, title: "Return to Cloudbreak", wordCount: 812 },
  { index: 2, title: "Elder Qiu's warning", wordCount: 1104 },
];

describe("ChapterPreviewTable", () => {
  it("shows an empty message when the preset matched no chapters", () => {
    render(<ChapterPreviewTable chapters={[]} selected={null} onToggle={vi.fn()} />);
    expect(screen.getByText(/no chapters matched/i)).toBeInTheDocument();
  });

  it("renders index, title and word count for every chapter", () => {
    render(<ChapterPreviewTable chapters={chapters} selected={null} onToggle={vi.fn()} />);
    expect(screen.getByText("Return to Cloudbreak")).toBeInTheDocument();
    expect(screen.getByText("812")).toBeInTheDocument();
    expect(screen.getByText("1104")).toBeInTheDocument();
  });

  it("defaults every chapter to checked when selected is null", () => {
    render(<ChapterPreviewTable chapters={chapters} selected={null} onToggle={vi.fn()} />);
    for (const checkbox of screen.getAllByRole("checkbox")) {
      expect(checkbox).toBeChecked();
    }
  });

  it("only checks chapters present in an explicit selection set", () => {
    render(<ChapterPreviewTable chapters={chapters} selected={new Set([1])} onToggle={vi.fn()} />);
    expect(screen.getByLabelText("Include chapter 1")).toBeChecked();
    expect(screen.getByLabelText("Include chapter 2")).not.toBeChecked();
  });

  it("calls onToggle with the chapter index when its checkbox is clicked", () => {
    const onToggle = vi.fn();
    render(<ChapterPreviewTable chapters={chapters} selected={null} onToggle={onToggle} />);
    fireEvent.click(screen.getByLabelText("Include chapter 2"));
    expect(onToggle).toHaveBeenCalledWith(2);
  });
});
