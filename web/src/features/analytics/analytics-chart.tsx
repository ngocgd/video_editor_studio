import { useEffect, useRef, useState } from "react";

import type { ChartHandle, ChartSpec } from "./uplot-render";

export type { ChartSeries, ChartSpec } from "./uplot-render";

/** Converts a YYYY-MM-DD day to unix seconds at UTC midnight (the chart's x value for that day). */
export function dayToUnix(isoDate: string): number {
  return Date.parse(`${isoDate}T00:00:00Z`) / 1000;
}

/**
 * Line chart backed by uPlot, which is imported on first render so it stays
 * out of the route's static bundle. The chart is redrawn when `spec` changes
 * and follows the container width.
 */
export function AnalyticsChart({ spec, ariaLabel }: { spec: ChartSpec; ariaLabel: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let handle: ChartHandle | undefined;
    let cancelled = false;
    const observer = new ResizeObserver((entries) => {
      const width = Math.floor(entries[0]?.contentRect.width ?? 0);
      if (width > 0) handle?.setWidth(width);
    });

    import("./uplot-render")
      .then(({ renderChart }) => {
        if (cancelled) return;
        container.replaceChildren();
        handle = renderChart(container, spec, Math.max(container.clientWidth, 240));
        observer.observe(container);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });

    return () => {
      cancelled = true;
      observer.disconnect();
      handle?.destroy();
    };
  }, [spec]);

  if (failed) {
    return <p className="text-sm text-text-2">The chart could not be loaded. Reload the page to try again.</p>;
  }
  return <div ref={containerRef} role="img" aria-label={ariaLabel} className="w-full text-xs" style={{ minHeight: spec.height }} />;
}
