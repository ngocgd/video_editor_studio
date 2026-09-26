import { useState } from "react";

import type { BibleSection } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { GlossaryTable, isGlossaryTable } from "./glossary-table";

const SECTION_LABELS: Record<string, string> = {
  world: "World",
  cultivation_realms: "Cultivation realms",
  arcs: "Arcs",
  style_guide: "Style guide",
  running_summary: "Running summary",
  glossary: "Glossary",
};

/**
 * One editable bible section: origin/tainted badge, PATCH with
 * expectedVersion. The draft is seeded from the section once; the parent
 * keys this component by section and version so a newer version (a reload
 * after a conflict, or a save) starts a fresh draft.
 */
export function BibleSectionEditor({
  sectionKey,
  section,
  onSave,
  saving,
}: {
  sectionKey: string;
  section: BibleSection;
  onSave: (content: string) => void;
  saving: boolean;
}) {
  const [draft, setDraft] = useState(section.content);
  const dirty = draft !== section.content;
  const asTable = sectionKey === "glossary" && isGlossaryTable(section.content);

  return (
    <section className="flex flex-col gap-2 rounded-md border border-border bg-card p-3">
      <div className="flex items-center gap-2">
        <h3 className="text-md font-medium">{SECTION_LABELS[sectionKey] ?? sectionKey}</h3>
        <span
          className={`rounded-sm px-1.5 py-0.5 text-xs ${
            section.origin === "model" ? "bg-info-muted text-info" : section.origin === "import" ? "bg-warning-muted text-warning" : "bg-muted text-text-2"
          }`}
        >
          {section.origin}
        </span>
        {section.tainted && <span className="rounded-sm bg-warning-muted px-1.5 py-0.5 text-xs text-warning">tainted</span>}
        <span className="ml-auto font-mono text-xs text-muted-foreground">v{section.version}</span>
      </div>

      {asTable ? (
        <GlossaryTable content={draft} onSave={(next) => onSave(next)} />
      ) : (
        <>
          {sectionKey === "glossary" && (
            <p className="text-xs text-text-2">This glossary is not in table form, so it is edited as text.</p>
          )}
          <textarea
            className="min-h-32 w-full rounded-md border border-input bg-well px-2.5 py-2 text-sm text-foreground"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            maxLength={20000}
          />
          <Button variant="primary" size="sm" className="w-fit" disabled={!dirty || saving} onClick={() => onSave(draft)}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </>
      )}
    </section>
  );
}
