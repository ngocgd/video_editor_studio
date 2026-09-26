import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";

import type { SceneTake } from "../../api/gen/types.gen";
import { VirtualGrid } from "../../components/shared/virtual-grid";
import { SceneFilterChips } from "./scene-filter-chips";
import { columnsForWidth, nextMotion, rangeSelection, tileHeight, toPipelinePips } from "./storyboard-model";
import { TakesStrip } from "./takes-strip";

describe("grid keyboard roving", () => {
  let rectSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    // Give the scroll container a size so the virtualizer renders rows.
    rectSpy = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      width: 800, height: 600, top: 0, left: 0, right: 800, bottom: 600, x: 0, y: 0, toJSON: () => ({}),
    });
    Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, get: () => 600 });
    Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, get: () => 800 });
  });
  afterEach(() => rectSpy.mockRestore());

  function Harness({ onChange }: { onChange: (i: number) => void }) {
    const [active, setActive] = useState(0);
    const items = Array.from({ length: 10 }, (_, i) => ({ id: `s${i}` }));
    return (
      <VirtualGrid
        items={items}
        columns={4}
        rowHeight={100}
        getItemId={(s) => s.id}
        activeIndex={active}
        onActiveIndexChange={(i) => {
          setActive(i);
          onChange(i);
        }}
        ariaLabel="Scenes"
        renderItem={(s, _i, isActive) => <span data-active={isActive}>{s.id}</span>}
      />
    );
  }

  it("moves one focus stop with the arrow keys, clamped to the grid", () => {
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const grid = screen.getByRole("grid", { name: "Scenes" });
    expect(grid).toHaveAttribute("tabindex", "0");
    expect(grid).toHaveAttribute("aria-activedescendant", "cell-s0");
    fireEvent.keyDown(grid, { key: "ArrowRight" });
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    fireEvent.keyDown(grid, { key: "ArrowDown" });
    fireEvent.keyDown(grid, { key: "ArrowLeft" });
    fireEvent.keyDown(grid, { key: "End" });
    fireEvent.keyDown(grid, { key: "ArrowRight" });
    fireEvent.keyDown(grid, { key: "Home" });
    fireEvent.keyDown(grid, { key: "ArrowUp" });
    expect(onChange.mock.calls.map((c) => c[0])).toEqual([1, 5, 9, 8, 9, 9, 0, 0]);
    expect(grid).toHaveAttribute("aria-activedescendant", "cell-s0");
    // Only one cell is marked active: a single focus stop.
    expect(screen.getAllByRole("gridcell").filter((c) => c.getAttribute("aria-selected") === "true")).toHaveLength(1);
  });

  it("derives columns from the container width", () => {
    expect(columnsForWidth(0)).toBe(1);
    expect(columnsForWidth(271)).toBe(1);
    expect(columnsForWidth(552)).toBe(2);
    expect(columnsForWidth(1000)).toBe(3);
    expect(tileHeight(272)).toBe(Math.ceil((260 * 9) / 16) + 112);
  });
});

describe("filter chips", () => {
  it("shows the server's counts and marks the active filter", async () => {
    const onChange = vi.fn();
    const { container } = render(<SceneFilterChips value="stale" counts={{ all: 48, stale: 3, failed: 1, missing: 15, inQueue: 4 }} onChange={onChange} />);
    expect(screen.getByRole("button", { name: "All 48" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: "Stale 3" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Missing 15" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "In queue 4" }));
    expect(onChange).toHaveBeenCalledWith("in_queue");
    expect(await axe(container)).toHaveNoViolations();
  });
});

function take(id: string, overrides: Partial<SceneTake> = {}): SceneTake {
  return { id, kind: "image", assetId: `asset-${id}`, selected: false, stale: false, createdAt: "2026-09-26T00:00:00Z", params: {}, variants: true, ...overrides };
}

describe("takes strip", () => {
  it("compares the selected take A/B with another and reverts by selecting it", () => {
    const onSelect = vi.fn();
    render(<TakesStrip kind="image" takes={[take("t1"), take("t2", { selected: true, stale: true }), take("v1", { kind: "voice" })]} onSelect={onSelect} />);
    // Only this kind's takes, numbered, the selected one marked.
    expect(screen.getAllByRole("radio").map((r) => r.textContent)).toEqual(["T1", "T2·stale"]);
    expect(screen.queryByRole("button", { name: /A\/B/ })).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: "T1" }));
    fireEvent.click(screen.getByRole("button", { name: /A\/B/ }));
    const ab = screen.getByTestId("takes-ab");
    expect(ab).toHaveTextContent("A · T2 (selected)");
    expect(ab).toHaveTextContent("B · T1");
    fireEvent.click(screen.getByRole("button", { name: "Use take T1" }));
    expect(onSelect).toHaveBeenCalledWith("t1");
  });

  it("says so when there are no takes", () => {
    render(<TakesStrip kind="voice" takes={[take("t1")]} onSelect={() => {}} />);
    expect(screen.getByText("No takes yet.")).toBeInTheDocument();
  });
});

describe("storyboard model", () => {
  it("selects an inclusive range in either direction", () => {
    const scenes = ["a", "b", "c", "d"].map((id) => ({ id }));
    expect([...rangeSelection(scenes, 1, 3)]).toEqual(["b", "c", "d"]);
    expect([...rangeSelection(scenes, 3, 0)]).toEqual(["a", "b", "c", "d"]);
  });

  it("turns pips into labelled chips with stale and failure reasons", () => {
    const pips = toPipelinePips([
      { kind: "voice", state: "stale", staleReason: "Narration edited after this take was generated" },
      { kind: "image", state: "failed", errorCode: "engine_not_installed", errorMessage: "" },
      { kind: "align", state: "running", progress: 40 },
    ]);
    expect(pips.map((p) => p.label)).toEqual(["VOI", "IMG", "SUB"]);
    expect(pips[0].staleReason).toMatch(/Narration edited/);
    expect(pips[1].staleReason).toBe("IMG failed: engine_not_installed");
    expect(pips[2].detail).toBe("40%");
  });

  it("cycles the motion presets", () => {
    expect(nextMotion("ken_burns")).toBe("parallax");
    expect(nextMotion("static")).toBe("ken_burns");
  });
});
