import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { CleanupPreview } from "../../api/gen/types.gen";
import { CleanupConfirmDialog } from "./library-cleanup";
import { previewSummary, referenceLabel, usageShares } from "./library-model";
import { RetentionForm } from "./library-view";

const PREVIEW: CleanupPreview = {
  settings: { segmentTtlDays: 30, takeTtlDays: 14 },
  segments: [{ id: "abc", kind: "segment", assetId: "a1", bytes: 2 * 1024 ** 3, since: "2026-08-01T00:00:00Z" }],
  takes: [
    { id: "t1", kind: "image", assetId: "a2", bytes: 512 * 1024 ** 2, since: "2026-08-01T00:00:00Z" },
    { id: "t2", kind: "voice", assetId: "a3", bytes: 512 * 1024 ** 2, since: "2026-08-01T00:00:00Z" },
  ],
  bytes: 3 * 1024 ** 3,
  truncated: false,
  token: "tok",
};

describe("library model", () => {
  it("labels references and unused assets", () => {
    expect(referenceLabel([])).toBe("Unused");
    expect(referenceLabel(["selected_take", "render"])).toBe("Selected take, Render");
  });

  it("orders usage by size with shares", () => {
    const rows = usageShares(
      [
        { seriesId: "s1", seriesTitle: "Small", assets: 1, bytes: 25 },
        { assets: 2, bytes: 75 },
      ],
      100,
    );
    expect(rows.map((r) => [r.label, r.share])).toEqual([
      ["Not in a project", 0.75],
      ["Small", 0.25],
    ]);
  });

  it("sums up a preview", () => {
    expect(previewSummary(PREVIEW)).toBe("1 expired render cache entry and 2 unselected takes, 3.0 GB in total.");
    expect(previewSummary({ ...PREVIEW, truncated: true })).toContain("the first batch");
  });
});

describe("CleanupConfirmDialog", () => {
  it("confirms the previewed set", () => {
    const onConfirm = vi.fn();
    render(<CleanupConfirmDialog preview={PREVIEW} pending={false} onConfirm={onConfirm} onCancel={vi.fn()} />);
    expect(screen.getByText(/This deletes 1 expired render cache entry/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Delete 3.0 GB" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("cannot confirm an empty preview and shows the changed-set notice", () => {
    render(<CleanupConfirmDialog preview={{ ...PREVIEW, segments: [], takes: [], bytes: 0 }} notice="What can be deleted changed." pending={false} onConfirm={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByText(/Nothing has expired/)).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("What can be deleted changed.");
    expect(screen.getByRole("button", { name: /Delete/ })).toBeDisabled();
  });
});

describe("RetentionForm", () => {
  it("saves the edited retention days", () => {
    const onSave = vi.fn();
    render(<RetentionForm settings={{ segmentTtlDays: 30, takeTtlDays: 14 }} saving={false} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText(/Keep unselected takes/), { target: { value: "7" } });
    fireEvent.click(screen.getByRole("button", { name: "Save retention" }));
    expect(onSave).toHaveBeenCalledWith({ segmentTtlDays: 30, takeTtlDays: 7 });
  });
});
