import { Circle, CircleCheck, CircleDashed, CircleX, Clock, Loader, TriangleAlert } from "lucide-react";

import { cn } from "../../lib/cn";

export type EntityState = "done" | "running" | "queued" | "failed" | "stale" | "none";

const STATE_META: Record<EntityState, { label: string; icon: typeof Circle; className: string }> = {
  done: { label: "Done", icon: CircleCheck, className: "bg-success-muted text-success" },
  running: { label: "Running", icon: Loader, className: "bg-info-muted text-info" },
  queued: { label: "Queued", icon: Clock, className: "bg-muted text-text-2" },
  failed: { label: "Failed", icon: CircleX, className: "bg-destructive-muted text-destructive" },
  stale: { label: "Stale", icon: TriangleAlert, className: "bg-warning-muted text-warning" },
  none: { label: "Not started", icon: CircleDashed, className: "bg-muted text-muted-foreground" },
};

/**
 * Status is never colour-only (guidelines §2.2/§9): every chip pairs an
 * icon fixed to its meaning with a word. `detail` appends a short suffix,
 * e.g. "43%" for running or "#3" for queued.
 */
export function StatusChip({
  state,
  detail,
  label,
  className,
}: {
  state: EntityState;
  /** Appended after the label, e.g. "43%" for running or "#3" for queued. */
  detail?: string;
  /** Overrides the default state word, e.g. a pipeline pip's step name ("IMG"). */
  label?: string;
  className?: string;
}) {
  const meta = STATE_META[state];
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-normal",
        meta.className,
        className,
      )}
    >
      <Icon
        size={14}
        strokeWidth={1.75}
        className={state === "running" ? "animate-spin motion-reduce:animate-none" : undefined}
        aria-hidden="true"
      />
      {label ?? meta.label}
      {detail ? ` ${detail}` : ""}
    </span>
  );
}

/** Worst-state rollup used by row-level pips (guidelines: "Row-level status uses the worst state"). */
export function worstState(states: EntityState[]): EntityState {
  const order: EntityState[] = ["failed", "stale", "running", "queued", "none", "done"];
  for (const candidate of order) {
    if (states.includes(candidate)) return candidate;
  }
  return "none";
}
