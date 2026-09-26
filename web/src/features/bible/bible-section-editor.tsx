import { useState } from "react";

import type { BibleSection } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { GlossaryTable } from "./glossary-table";

const SECTION_LABELS: Record<string, string> = {
  world: "World",
  cultivation_realms: "Cultivation realms",
  arcs: "Arcs",
  style_guide: "Style guide",
  running_summary: "Running summary",
  glossary: "Glossary",
};

/** One editable bible section (phase 6): origin/tainted badge, PATCH with expectedVersion. */
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

      {sectionKey === "glossary" ? (
        <GlossaryTable content={draft} onSave={(next) => onSave(next)} />
      ) : (
        <>
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
