import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef, type ReactNode } from "react";

/**
 * TanStack Virtual wrapper for the storyboard scene grid: renders only
 * visible rows of a fixed-column grid with roving tabindex (guidelines §9).
 */
export function VirtualGrid<T>({
  items,
  columns,
  rowHeight,
  getItemId,
  activeIndex,
  onActiveIndexChange,
  renderItem,
  ariaLabel,
}: {
  items: T[];
  columns: number;
  rowHeight: number;
  getItemId: (item: T) => string;
  activeIndex: number;
  onActiveIndexChange: (index: number) => void;
  renderItem: (item: T, index: number, isActive: boolean) => ReactNode;
  ariaLabel: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const rowCount = Math.ceil(items.length / columns);
  const rowVirtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 3,
  });

  function onKeyDown(event: React.KeyboardEvent) {
    if (items.length === 0) return;
    const deltas: Record<string, number> = {
      ArrowRight: 1,
      ArrowLeft: -1,
      ArrowDown: columns,
      ArrowUp: -columns,
    };
    const delta = deltas[event.key];
    if (delta == null) return;
    event.preventDefault();
    const next = Math.max(0, Math.min(items.length - 1, activeIndex + delta));
    onActiveIndexChange(next);
    rowVirtualizer.scrollToIndex(Math.floor(next / columns));
  }

  return (
    <div
      ref={scrollRef}
      role="grid"
      aria-label={ariaLabel}
      tabIndex={0}
      onKeyDown={onKeyDown}
      className="max-h-[720px] overflow-auto"
    >
      <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative" }}>
        {rowVirtualizer.getVirtualItems().map((virtualRow) => {
          const start = virtualRow.index * columns;
          const rowItems = items.slice(start, start + columns);
          return (
            <div
              key={virtualRow.key}
              role="row"
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                height: virtualRow.size,
                transform: `translateY(${virtualRow.start}px)`,
                gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
              }}
              className="grid gap-2"
            >
              {rowItems.map((item, columnIndex) => {
                const index = start + columnIndex;
                return (
                  <div key={getItemId(item)} role="gridcell" aria-selected={index === activeIndex}>
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
