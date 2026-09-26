import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StoryBible } from "../../api/gen/types.gen";
import { BibleEditorView } from "./bible-editor-view";

const mutate = vi.fn();
let bible: StoryBible;

vi.mock("./use-bible", () => ({
  useBible: () => ({ data: bible, refetch: vi.fn() }),
  useUpdateBibleSection: () => ({ mutate, isPending: false }),
  isVersionConflict: () => false,
}));

function bibleWith(sections: StoryBible["sections"]): StoryBible {
  return { seriesId: "s1", sections, updatedAt: "2026-09-26T00:00:00Z" };
}

describe("BibleEditorView", () => {
  beforeEach(() => {
    mutate.mockReset();
  });

  it("shows every section and saves a missing one at version 0", () => {
    bible = bibleWith({ world: { content: "A sect.", origin: "user", tainted: false, version: 3 } });
    render(<BibleEditorView seriesId="s1" />);

    for (const heading of ["World", "Cultivation realms", "Arcs", "Style guide", "Running summary", "Glossary"]) {
      expect(screen.getByRole("heading", { name: heading })).toBeInTheDocument();
    }

    const arcs = screen.getByRole("heading", { name: "Arcs" }).closest("section") as HTMLElement;
    const textarea = arcs.querySelector("textarea") as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "Outer disciple to elder." } });
    const saveButton = Array.from(arcs.querySelectorAll("button")).find((b) => b.textContent === "Save") as HTMLButtonElement;
    fireEvent.click(saveButton);

    expect(mutate).toHaveBeenCalledWith({ section: "arcs", content: "Outer disciple to elder.", expectedVersion: 0 }, expect.anything());
  });

  it("replaces an unsaved draft with the newer version after a reload", () => {
    bible = bibleWith({ world: { content: "Old world.", origin: "user", tainted: false, version: 1 } });
    const { rerender } = render(<BibleEditorView seriesId="s1" />);
    const worldArea = () => (screen.getByRole("heading", { name: "World" }).closest("section") as HTMLElement).querySelector("textarea") as HTMLTextAreaElement;

    fireEvent.change(worldArea(), { target: { value: "My stale edit." } });
    expect(worldArea().value).toBe("My stale edit.");

    bible = bibleWith({ world: { content: "Someone else's world.", origin: "user", tainted: false, version: 2 } });
    rerender(<BibleEditorView seriesId="s1" />);

    expect(worldArea().value).toBe("Someone else's world.");
  });

  it("edits a prose glossary as text instead of an empty table", () => {
    bible = bibleWith({ glossary: { content: "Qi: life energy.", origin: "user", tainted: false, version: 1 } });
    render(<BibleEditorView seriesId="s1" />);
    const glossary = screen.getByRole("heading", { name: "Glossary" }).closest("section") as HTMLElement;

    expect(glossary.querySelector("table")).toBeNull();
    expect((glossary.querySelector("textarea") as HTMLTextAreaElement).value).toBe("Qi: life energy.");
  });

  it("shows a JSON term glossary as a table", () => {
    bible = bibleWith({
      glossary: { content: JSON.stringify([{ termZh: "气", en: "Qi", vi: "Khí" }]), origin: "model", tainted: false, version: 1 },
    });
    render(<BibleEditorView seriesId="s1" />);
    const glossary = screen.getByRole("heading", { name: "Glossary" }).closest("section") as HTMLElement;

    expect(glossary.querySelector("table")).not.toBeNull();
    expect(screen.getByDisplayValue("气")).toBeInTheDocument();
  });
});
