/**
 * Formatting and availability rules for analytics figures. A metric the
 * YouTube APIs did not return is absent from the payload (never 0), so every
 * cell goes through `metricCell`, which turns an absent value into an
 * explicit "not available" or "pending" label instead of a fake zero.
 */

export const NOT_AVAILABLE = "Not available from API";
export const REACH_PENDING = "Pending: first reach report not yet generated";

/** Metrics that only come from the Reporting API reach report. */
const REACH_METRICS = new Set(["impressions", "ctr"]);

export type MetricKind = "count" | "hours" | "percent" | "ratioPercent" | "seconds";

export interface MetricCell {
  text: string;
  /** Why the value is missing (tooltip text); undefined when a value is shown. */
  reason?: string;
  missing: boolean;
}

const countFormat = new Intl.NumberFormat("en-US");
const compactFormat = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });

export function formatCount(value: number): string {
  return Math.abs(value) >= 100_000 ? compactFormat.format(value) : countFormat.format(value);
}

export function formatHours(value: number): string {
  return `${value >= 100 ? Math.round(value).toLocaleString("en-US") : value.toFixed(1)} h`;
}

/** Formats a percentage that is already expressed in percent (0-100), e.g. averageViewPercentage. */
export function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`;
}

/** Formats a 0-1 ratio as a percentage, e.g. CTR from the reach report. */
export function formatRatioPercent(value: number): string {
  return `${(value * 100).toFixed(2)}%`;
}

export function formatSeconds(value: number): string {
  const safe = Math.max(0, Math.round(value));
  const minutes = Math.floor(safe / 60);
  const seconds = safe % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}

function formatMetric(kind: MetricKind, value: number): string {
  switch (kind) {
    case "count":
      return formatCount(value);
    case "hours":
      return formatHours(value);
    case "percent":
      return formatPercent(value);
    case "ratioPercent":
      return formatRatioPercent(value);
    case "seconds":
      return formatSeconds(value);
  }
}

/**
 * Renders one metric value. `metric` is the YouTube API metric name used as
 * the key of `unavailable`; `reachThrough` is the sync state's reach report
 * date, absent until the first reach report has been ingested.
 */
export function metricCell(
  value: number | undefined,
  kind: MetricKind,
  metric: string,
  context: { reachThrough?: string; unavailable?: Record<string, string> } = {},
): MetricCell {
  if (value !== undefined && Number.isFinite(value)) {
    return { text: formatMetric(kind, value), missing: false };
  }
  if (REACH_METRICS.has(metric) && !context.reachThrough) {
    return { text: REACH_PENDING, missing: true, reason: "YouTube generates the first reach report up to 48 hours after the reporting job is created." };
  }
  return { text: NOT_AVAILABLE, missing: true, reason: context.unavailable?.[metric] };
}

/** Formats an ISO date (YYYY-MM-DD) for "data through" stamps without a timezone shift. */
export function formatDay(isoDate: string): string {
  const [year, month, day] = isoDate.split("-").map(Number);
  if (!year || !month || !day) return isoDate;
  return new Date(Date.UTC(year, month - 1, day)).toLocaleDateString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

/** Renders an evidence value from a suggestion (numbers rounded, arrays joined). */
export function formatEvidenceValue(value: unknown): string {
  if (typeof value === "number") {
    return Number.isInteger(value) ? countFormat.format(value) : value.toFixed(3).replace(/\.?0+$/, "");
  }
  if (Array.isArray(value)) return value.map(formatEvidenceValue).join(", ");
  if (value === null || value === undefined) return "n/a";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

/** Turns a camelCase or snake_case evidence key into a readable label. */
export function evidenceLabel(key: string): string {
  const spaced = key.replace(/_/g, " ").replace(/([a-z0-9])([A-Z])/g, "$1 $2").toLowerCase();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
