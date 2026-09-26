/**
 * The only module that imports uPlot. It is loaded with a dynamic import from
 * `analytics-chart.tsx`, so uPlot and its stylesheet live in a lazy chunk and
 * never count against the first paint of the Analytics route.
 */
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";

export interface ChartSeries {
  label: string;
  /** One value per x; null leaves a gap (a metric the API did not return is never drawn as 0). */
  values: (number | null)[];
  color: string;
  /** Formats a value for the legend and the y axis. */
  format: (value: number) => string;
  /** Series sharing a scale share the y axis; defaults to "y". */
  scale?: string;
  dash?: number[];
}

export interface ChartSpec {
  /** "time" x values are unix seconds (UTC midnight of each day); "ratio" x values are 0..1. */
  xKind: "time" | "ratio";
  x: number[];
  series: ChartSeries[];
  height: number;
}

export interface ChartHandle {
  setWidth(width: number): void;
  destroy(): void;
}

function cssVar(name: string, fallback: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

export function renderChart(target: HTMLElement, spec: ChartSpec, width: number): ChartHandle {
  const grid = cssVar("--border", "#34363c");
  const text = cssVar("--text-2", "#a8a6a0");
  const scales = [...new Set(spec.series.map((s) => s.scale ?? "y"))];

  const options: uPlot.Options = {
    width,
    height: spec.height,
    // Dates are UTC days: format them in UTC so a viewer's timezone never shifts a bar to the previous day.
    tzDate: (ts) => uPlot.tzDate(new Date(ts * 1000), "Etc/UTC"),
    scales: {
      x: spec.xKind === "time" ? { time: true } : { time: false, range: [0, 1] },
      ...Object.fromEntries(scales.map((key) => [key, { auto: true }])),
    },
    axes: [
      {
        stroke: text,
        grid: { stroke: grid, width: 1 },
        ticks: { stroke: grid, width: 1 },
        ...(spec.xKind === "ratio" ? { values: (_u: uPlot, ticks: number[]) => ticks.map((t) => `${Math.round(t * 100)}%`) } : {}),
      },
      ...scales.map((scale, index) => {
        const first = spec.series.find((s) => (s.scale ?? "y") === scale);
        return {
          scale,
          side: index === 0 ? 3 : 1,
          stroke: text,
          grid: { stroke: grid, width: 1, show: index === 0 },
          ticks: { stroke: grid, width: 1 },
          values: (_u: uPlot, ticks: number[]) => ticks.map((t) => (first ? first.format(t) : String(t))),
          size: 56,
        } satisfies uPlot.Axis;
      }),
    ],
    series: [
      spec.xKind === "ratio" ? { label: "Elapsed", value: (_u, v) => (v == null ? "" : `${Math.round(v * 100)}%`) } : {},
      ...spec.series.map((s) => ({
        label: s.label,
        scale: s.scale ?? "y",
        stroke: s.color,
        width: 1.5,
        dash: s.dash,
        spanGaps: false,
        points: { show: false },
        value: (_u: uPlot, v: number | null) => (v == null ? "n/a" : s.format(v)),
      })),
    ],
    legend: { show: true },
    cursor: { drag: { x: false, y: false } },
  };

  const chart = new uPlot(options, [spec.x, ...spec.series.map((s) => s.values)] as uPlot.AlignedData, target);
  return {
    setWidth: (next) => chart.setSize({ width: next, height: spec.height }),
    destroy: () => chart.destroy(),
  };
}
