import type { OutlineBeat } from "../../api/gen/types.gen";
import { InspectorPanel, InspectorSection } from "../../components/shared/inspector-panel";
import { Button } from "../../components/ui/button";

/** Right pane (phase 6 writer layout): outline beats + a disabled characters placeholder (the pinning hook stays empty until phase 7). */
export function WriterBeatsPanel({ outline, onExpandBeat }: { outline: OutlineBeat[]; onExpandBeat: (beatId: string) => void }) {
  return (
    <InspectorPanel title="Outline">
      <InspectorSection title="Beats">
        <ul className="flex flex-col gap-2">
          {outline.map((beat, index) => (
            <li key={beat.id} className="rounded-md border border-border bg-card p-2 text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-xs text-muted-foreground">{String(index + 1).padStart(2, "0")}</span>
                <span className="font-mono text-xs text-muted-foreground">{beat.targetWords}w</span>
              </div>
              <p className="mt-1 text-text-2">{beat.summary}</p>
              <Button variant="ghost" size="sm" className="mt-1" onClick={() => onExpandBeat(beat.id)}>
                Expand
              </Button>
            </li>
          ))}
          {outline.length === 0 && <p className="text-sm text-text-2">No beats yet.</p>}
        </ul>
      </InspectorSection>

      <InspectorSection title="Characters">
        <div className="flex flex-col gap-1 rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground opacity-60">
          <p>Character pinning is not available yet.</p>
          <p>This panel activates once characters (phase 7) are wired into story context.</p>
        </div>
      </InspectorSection>
    </InspectorPanel>
  );
}
