import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { axe } from "vitest-axe";

import { StatusChip, worstState } from "./status-chip";

describe("StatusChip", () => {
  it("always pairs the state with a text word, never colour only", () => {
    render(<StatusChip state="failed" />);
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("appends the detail suffix after the label", () => {
    render(<StatusChip state="running" detail="43%" />);
    expect(screen.getByText("Running 43%")).toBeInTheDocument();
  });

  it("has no obvious accessibility violations", async () => {
    const { container } = render(<StatusChip state="stale" />);
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});

describe("worstState", () => {
  it("prioritises failed over every other state", () => {
    expect(worstState(["done", "running", "failed"])).toBe("failed");
  });

  it("falls back to none for an empty list", () => {
    expect(worstState([])).toBe("none");
  });
});
