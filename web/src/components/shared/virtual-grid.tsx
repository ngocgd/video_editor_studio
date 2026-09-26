import { useVirtualizer } from "@tanstack/react-virtual";
import { type ReactNode, useEffect, useRef } from "react";

/**
 * TanStack Virtual wrapper for the storyboard scene grid: renders only the
 * visible rows of a fixed-column grid (fixed row height, so nothing is
 * measured) with one roving focus stop (guidelines §9): the grid itself
 * holds focus and arrow keys move the active cell, announced through
 * aria-activedescendant.
 */
export function VirtualGrid<T>({
  items,
  columns,
  rowHeight,
  gap = 8,
  getItemId,
  activeIndex,
  onActiveIndexChange,
  renderItem,
  ariaLabel,
  className = "max-h-[720px]",
  overscan = 2,
}: {
  items: T[];
  columns: number;
  rowHeight: number;
  gap?: number;
  getItemId: (item: T) => string;
  activeIndex: number;
  onActiveIndexChange: (index: number) => void;
  renderItem: (item: T, index: number, isActive: boolean) => ReactNode;
  ariaLabel: string;
  className?: string;
  overscan?: number;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const rowCount = Math.ceil(items.length / columns);
  const rowVirtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight + gap,
    overscan,
  });

  // Keep the active cell in view when it changes from outside (J/K, timeline clicks).
  useEffect(() => {
    if (activeIndex >= 0 && activeIndex < items.length) {
      rowVirtualizer.scrollToIndex(Math.floor(activeIndex / columns), { align: "auto" });
    }
  }, [activeIndex, columns, items.length, rowVirtualizer]);

  function onKeyDown(event: React.KeyboardEvent) {
    if (items.length === 0) return;
    const deltas: Record<string, number> = {
      ArrowRight: 1,
      ArrowLeft: -1,
      ArrowDown: columns,
      ArrowUp: -columns,
    };
    let next: number | undefined;
    if (event.key in deltas) next = activeIndex + deltas[event.key];
    if (event.key === "Home") next = 0;
    if (event.key === "End") next = items.length - 1;
    if (next == null) return;
    event.preventDefault();
    onActiveIndexChange(Math.max(0, Math.min(items.length - 1, next)));
  }

  const activeId = items[activeIndex] ? `cell-${getItemId(items[activeIndex])}` : undefined;
  return (
    <div
      ref={scrollRef}
      role="grid"
      aria-label={ariaLabel}
      aria-rowcount={rowCount}
      aria-activedescendant={activeId}
      tabIndex={0}
      onKeyDown={onKeyDown}
      className={`overflow-auto ${className}`}
    >
      <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative" }}>
        {rowVirtualizer.getVirtualItems().map((virtualRow) => {
          const start = virtualRow.index * columns;
          const rowItems = items.slice(start, start + columns);
          return (
            <div
              key={virtualRow.key}
              role="row"
              aria-rowindex={virtualRow.index + 1}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                height: rowHeight,
                transform: `translateY(${virtualRow.start}px)`,
                gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
                columnGap: gap,
              }}
              className="grid"
            >
              {rowItems.map((item, columnIndex) => {
                const index = start + columnIndex;
                const id = getItemId(item);
                return (
                  <div key={id} id={`cell-${id}`} role="gridcell" aria-selected={index === activeIndex} data-index={index}>
                    {renderItem(item, index, index === activeIndex)}
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>
    </div>
  );
}
