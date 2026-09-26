import { CircleCheck, CircleDashed, LoaderCircle, TriangleAlert } from "lucide-react";

import type { RenderStage } from "../../api/gen/types.gen";

const ICONS: Record<RenderStage["state"], { icon: typeof CircleCheck; className: string }> = {
  done: { icon: CircleCheck, className: "text-success" },
  running: { icon: LoaderCircle, className: "text-primary-text animate-spin" },
  partial: { icon: TriangleAlert, className: "text-warning" },
  missing: { icon: CircleDashed, className: "text-text-2" },
  idle: { icon: CircleDashed, className: "text-text-2" },
};

function detail(stage: RenderStage, encoder: string): string {
  switch (stage.key) {
    case "script":
      return stage.done ? "Draft ready" : "No draft";
    case "compose":
      return stage.state === "idle" ? "Waits for all scenes" : `${stage.done}/${stage.total} cached`;
    case "encode":
      return stage.done ? `Rendered · ${encoder}` : encoder;
    default:
      return `${stage.done}/${stage.total}`;
  }
}

/** Stage summary across the top of the render page (wireframe render-queue). */
export function RenderStageStrip({ stages, encoder }: { stages: RenderStage[]; encoder: string }) {
  return (
    <ol aria-label="Render stages" className="grid grid-cols-7 overflow-hidden rounded-md border border-border">
      {stages.map((st) => {
        const { icon: Icon, className } = ICONS[st.state];
        const pct = st.total > 0 ? Math.min(100, (st.done / st.total) * 100) : 0;
        return (
          <li key={st.key} className="flex flex-col gap-1.5 border-r border-border p-3 last:border-r-0" aria-label={`${st.label}: ${st.state}`}>
            <span className="flex items-center gap-1.5 text-sm font-medium">
              <Icon size={14} strokeWidth={1.75} className={className} aria-hidden="true" />
              {st.label}
            </span>
            <span className="font-mono text-xs tabular-nums text-text-2">{detail(st, encoder)}</span>
            <span className="h-1 overflow-hidden rounded-full bg-muted">
              <span className={`block h-full ${st.state === "partial" ? "bg-warning" : "bg-primary"}`} style={{ width: `${pct}%` }} />
            </span>
          </li>
        );
      })}
    </ol>
  );
}
