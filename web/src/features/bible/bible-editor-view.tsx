import { useState } from "react";

import type { BibleSectionUpdateRequest } from "../../api/gen/types.gen";
import { VersionConflictDialog } from "../../components/shared/version-conflict-dialog";
import { BibleSectionEditor } from "./bible-section-editor";
import { isVersionConflict, useBible, useUpdateBibleSection } from "./use-bible";

type SectionKey = BibleSectionUpdateRequest["section"];
const SECTION_ORDER: SectionKey[] = ["world", "cultivation_realms", "arcs", "style_guide", "running_summary", "glossary"];

/** Story bible editor (phase 6): world, cultivation realms, arcs, style guide, running summary, glossary. */
export function BibleEditorView({ seriesId }: { seriesId: string }) {
  const bibleQuery = useBible(seriesId);
  const update = useUpdateBibleSection(seriesId);
  const [conflict, setConflict] = useState(false);
  const [pendingSection, setPendingSection] = useState<{ section: SectionKey; content: string } | null>(null);

  const bible = bibleQuery.data;

  const save = (section: SectionKey, content: string) => {
    const current = bible?.sections[section];
    if (!current) return;
    setPendingSection({ section, content });
    update.mutate(
      { section, content, expectedVersion: current.version },
      {
        onError: (error) => {
          if (isVersionConflict(error)) {
            setConflict(true);
          }
        },
        onSuccess: () => setPendingSection(null),
      },
    );
  };

  if (!bible) return <p className="text-sm text-text-2">Loading…</p>;

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold">Story bible</h1>
      {SECTION_ORDER.map((key) => {
        const section = bible.sections[key];
        if (!section) return null;
        return (
          <BibleSectionEditor
            key={key}
            sectionKey={key}
            section={section}
            saving={update.isPending && pendingSection?.section === key}
            onSave={(content) => save(key, content)}
          />
        );
      })}

      <VersionConflictDialog
        open={conflict}
        onReload={() => {
          setConflict(false);
          setPendingSection(null);
          void bibleQuery.refetch();
        }}
        onDiscard={() => {
          setConflict(false);
          setPendingSection(null);
        }}
      />
    </div>
  );
}
