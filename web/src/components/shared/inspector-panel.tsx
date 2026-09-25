import type { ReactNode } from "react";

import { ScrollArea } from "../ui/scroll-area";

/** Context panel for the current selection; never a modal for per-item editing (guidelines §6). */
export function InspectorPanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <aside aria-label="Inspector" className="flex h-full flex-col border-l border-border bg-background">
      <div className="flex h-11 shrink-0 items-center border-b border-border px-3 text-md font-medium">
        {title}
      </div>
      <ScrollArea className="flex-1">
        <div className="flex flex-col gap-4 p-3">{children}</div>
      </ScrollArea>
    </aside>
  );
}

export function InspectorSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-2xs font-medium uppercase tracking-[0.04em] text-text-2">{title}</h3>
      {children}
    </section>
  );
}
