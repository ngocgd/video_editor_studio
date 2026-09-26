import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { axe } from "vitest-axe";

import type { ModelInfo } from "../../api/gen/types.gen";
import { downloadPercent, ModelStatus, rowAction } from "./model-manager-view";
import { modelsRefetchInterval } from "./use-models";

function model(overrides: Partial<ModelInfo> = {}): ModelInfo {
  return {
    name: "z-image-turbo",
    task: "scene",
    title: "Scene images",
    engine: "comfyui",
    licence: { spdx: "Apache-2.0", url: "https://example.com", verified: "2026-09-24", allowed: true },
    sizeBytes: 12_168_299_735,
    vramMb: 10_500,
    status: "not_installed",
    bytesDone: 0,
    bytesTotal: 12_168_299_735,
    loaded: false,
    overBudget: false,
    ...overrides,
  };
}

describe("ModelStatus", () => {
  it("shows a blocked licence as blocked, never installable", () => {
    const blocked = model({ status: "blocked", licence: { spdx: "LicenseRef-x", url: "u", verified: "d", allowed: false } });
    render(<ModelStatus model={blocked} />);
    expect(screen.getByText("Blocked: licence")).toBeInTheDocument();
    expect(rowAction(blocked, "owner")).toBeNull();
  });

  it("shows download progress with a percentage", () => {
    render(<ModelStatus model={model({ status: "downloading", bytesDone: 38, bytesTotal: 100 })} />);
    expect(screen.getByLabelText("Downloading 38%")).toBeInTheDocument();
  });

  it("distinguishes loaded from merely installed", () => {
    const { rerender } = render(<ModelStatus model={model({ status: "installed" })} />);
    expect(screen.getByText("Installed")).toBeInTheDocument();
    rerender(<ModelStatus model={model({ status: "installed", loaded: true })} />);
    expect(screen.getByText("Loaded")).toBeInTheDocument();
  });

  it("has no obvious accessibility violations", async () => {
    const { container } = render(<ModelStatus model={model({ status: "paused", bytesDone: 50, bytesTotal: 100 })} />);
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe("rowAction", () => {
  it("offers install, pause and resume to owners only", () => {
    expect(rowAction(model(), "owner")?.label).toBe("Install");
    expect(rowAction(model(), "editor")).toBeNull();
    expect(rowAction(model({ status: "downloading" }), "owner")?.action).toBe("pause");
    expect(rowAction(model({ status: "paused" }), "owner")?.label).toBe("Resume");
    expect(rowAction(model({ status: "failed" }), "owner")?.label).toBe("Retry");
  });

  it("offers load and unload to editors, nothing to viewers", () => {
    expect(rowAction(model({ status: "installed" }), "editor")?.action).toBe("load");
    expect(rowAction(model({ status: "installed", loaded: true }), "editor")?.action).toBe("unload");
    expect(rowAction(model({ status: "installed" }), "viewer")).toBeNull();
  });
});

describe("helpers", () => {
  it("computes a clamped download percentage", () => {
    expect(downloadPercent(model({ bytesDone: 1, bytesTotal: 3 }))).toBe(33);
    expect(downloadPercent(model({ bytesDone: 5, bytesTotal: 0 }))).toBe(0);
  });

  it("polls fast only while a download runs", () => {
    expect(modelsRefetchInterval({ items: [model({ status: "downloading" })] })).toBe(2000);
    expect(modelsRefetchInterval({ items: [model()] })).toBe(10_000);
  });
});
