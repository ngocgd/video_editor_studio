import type { SceneCounts, SceneFilter } from "../../api/gen/types.gen";
import { FILTERS } from "./storyboard-model";

/** All / Stale / Failed / Missing / In queue chips with the server's counts. */
export function SceneFilterChips({ value, counts, onChange }: { value: SceneFilter; counts?: SceneCounts; onChange: (filter: SceneFilter) => void }) {
  return (
    <div role="group" aria-label="Filter scenes" className="flex gap-1">
      {FILTERS.map((f) => (
        <button
          key={f.id}
          type="button"
          aria-pressed={value === f.id}
          onClick={() => onChange(f.id)}
          className={`flex h-7 items-center gap-1 rounded-full border px-2.5 text-xs ${value === f.id ? "border-primary bg-primary-muted" : "border-border hover:bg-accent"}`}
        >
          {f.label} <span className="font-mono tabular-nums">{counts?.[f.count] ?? 0}</span>
        </button>
      ))}
    </div>
  );
}
