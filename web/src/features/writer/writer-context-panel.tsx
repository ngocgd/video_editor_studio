import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { getBibleOptions } from "../../api/gen/@tanstack/react-query.gen";
import { InspectorPanel, InspectorSection } from "../../components/shared/inspector-panel";

const EXCERPT_SECTIONS = ["world", "running_summary"] as const;

/** Left pane (phase 6 writer layout): read-only bible excerpts for context, linking to the full bible editor. */
export function WriterContextPanel({ seriesId }: { seriesId: string }) {
  const bibleQuery = useQuery(getBibleOptions({ path: { id: seriesId } }));
  const sections = bibleQuery.data?.sections;

  return (
    <InspectorPanel title="Story bible">
      {EXCERPT_SECTIONS.map((key) => {
        const section = sections?.[key];
        if (!section) return null;
        return (
          <InspectorSection key={key} title={key === "world" ? "World" : "Previously"}>
            <p className="line-clamp-6 whitespace-pre-wrap text-sm text-text-2">{section.content || "Not written yet."}</p>
          </InspectorSection>
        );
      })}
      <Link to="/projects/$seriesId/bible" params={{ seriesId }} className="text-xs text-primary-text hover:underline">
        Open full bible
      </Link>
    </InspectorPanel>
  );
}
