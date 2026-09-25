import { cn } from "../../lib/cn";

/** Mono keycap for shortcuts (guidelines §8: 11px Plex Mono, popover fill, 1px border, radius sm). */
export function Kbd({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        "inline-flex min-w-5 items-center justify-center rounded-sm border border-border bg-popover px-1 font-mono text-[11px] text-text-2",
        className,
      )}
    >
      {children}
    </kbd>
  );
}
