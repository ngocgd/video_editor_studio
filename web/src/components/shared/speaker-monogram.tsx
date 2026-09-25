import { cn } from "../../lib/cn";

const SPEAKER_COLORS = [
  "var(--spk-narrator)",
  "var(--spk-1)",
  "var(--spk-2)",
  "var(--spk-3)",
  "var(--spk-4)",
  "var(--spk-5)",
] as const;

export interface Speaker {
  id: string;
  name: string;
  /** Index into the fixed speaker palette (0 = narrator); guidelines §2.3. */
  colorIndex: number;
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[1][0]).toUpperCase();
}

/** Two-letter monogram so speaker tracks stay identifiable without colour (guidelines §2.3). */
export function SpeakerMonogram({ speaker, className }: { speaker: Speaker; className?: string }) {
  const color = SPEAKER_COLORS[speaker.colorIndex % SPEAKER_COLORS.length];
  return (
    <span
      title={speaker.name}
      aria-label={speaker.name}
      className={cn(
        "inline-flex h-5 w-5 items-center justify-center rounded-full text-[10px] font-medium text-well",
        className,
      )}
      style={{ backgroundColor: color }}
    >
      {initials(speaker.name)}
    </span>
  );
}
