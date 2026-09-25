import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef, type ReactNode } from "react";

export interface VirtualTableColumn<T> {
  key: string;
  header: string;
  render: (row: T) => ReactNode;
  className?: string;
}

/**
 * TanStack Virtual wrapper with roving tabindex (guidelines §9: grids/lists
 * use roving tabindex with arrow keys). Renders only the visible row window
 * regardless of data size (budget: 10k rows -> <=60 DOM rows).
 */
export function VirtualTable<T>({
  rows,
  columns,
  rowHeight = 32,
  getRowId,
  activeIndex,
  onActiveIndexChange,
  onRowActivate,
  ariaLabel,
}: {
  rows: T[];
  columns: VirtualTableColumn<T>[];
  rowHeight?: number;
  getRowId: (row: T) => string;
  activeIndex: number;
  onActiveIndexChange: (index: number) => void;
  onRowActivate?: (row: T) => void;
  ariaLabel: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 8,
  });

  function onKeyDown(event: React.KeyboardEvent) {
    if (rows.length === 0) return;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      const next = Math.min(activeIndex + 1, rows.length - 1);
      onActiveIndexChange(next);
      virtualizer.scrollToIndex(next);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      const next = Math.max(activeIndex - 1, 0);
      onActiveIndexChange(next);
      virtualizer.scrollToIndex(next);
    } else if (event.key === "Enter" && onRowActivate) {
      onRowActivate(rows[activeIndex]);
    }
  }

  return (
    <div role="table" aria-label={ariaLabel} className="flex flex-col overflow-hidden rounded-md border border-border">
      <div role="row" className="flex border-b border-border bg-background text-2xs uppercase tracking-[0.04em] text-text-2">
        {columns.map((column) => (
          <div key={column.key} role="columnheader" className={column.className ?? "flex-1 px-3 py-2"}>
            {column.header}
          </div>
        ))}
      </div>
      <div ref={scrollRef} className="max-h-[560px] overflow-auto" tabIndex={0} onKeyDown={onKeyDown}>
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((virtualRow) => {
            const row = rows[virtualRow.index];
            const isActive = virtualRow.index === activeIndex;
            return (
              <div
                key={getRowId(row)}
                role="row"
                aria-selected={isActive}
                tabIndex={-1}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: virtualRow.size,
                  transform: `translateY(${virtualRow.start}px)`,
                }}
                className={`flex items-center border-b border-border text-sm ${isActive ? "bg-accent" : "hover:bg-card"}`}
                onClick={() => onActiveIndexChange(virtualRow.index)}
              >
                {columns.map((column) => (
                  <div key={column.key} role="cell" className={column.className ?? "flex-1 truncate px-3"}>
                    {column.render(row)}
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
