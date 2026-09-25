/**
 * The single timecode/duration/byte formatter (guidelines §3: "use
 * font-variant-numeric: tabular-nums for every timecode, counter and
 * progress figure"). Every screen that shows a timecode, ETA or file size
 * must go through these instead of re-implementing formatting.
 */

/** Formats whole seconds as `MM:SS` or `H:MM:SS` once past an hour. */
export function formatTimecode(totalSeconds: number): string {
  const safe = Number.isFinite(totalSeconds) && totalSeconds > 0 ? Math.floor(totalSeconds) : 0;
  const hours = Math.floor(safe / 3600);
  const minutes = Math.floor((safe % 3600) / 60);
  const seconds = safe % 60;
  const mm = minutes.toString().padStart(2, "0");
  const ss = seconds.toString().padStart(2, "0");
  return hours > 0 ? `${hours}:${mm}:${ss}` : `${mm}:${ss}`;
}

/** Formats an ETA in seconds as the mono `ETA MM:SS` label used in job rows. */
export function formatEta(etaSeconds: number | undefined | null): string | undefined {
  if (etaSeconds == null || !Number.isFinite(etaSeconds) || etaSeconds < 0) {
    return undefined;
  }
  return `ETA ${formatTimecode(etaSeconds)}`;
}

const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"] as const;

/** Formats a byte count as a human size with one decimal place above KB. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), BYTE_UNITS.length - 1);
  const value = bytes / 1024 ** exponent;
  const formatted = exponent === 0 ? value.toString() : value.toFixed(1);
  return `${formatted} ${BYTE_UNITS[exponent]}`;
}

/** Formats megabytes (as returned by the GPU status API) using formatBytes. */
export function formatMb(mb: number): string {
  return formatBytes(mb * 1024 * 1024);
}

/** Formats a percentage (0-100) rounded to the nearest whole number. */
export function formatPercent(value: number): string {
  return `${Math.round(value)}%`;
}

/**
 * Turns a raw step/run `kind` (a snake_case backend identifier, e.g.
 * "render_episode") into a human sentence-case label ("Render episode").
 * The raw kind should still be shown somewhere (mono, in a tooltip or
 * detail row) for debugging (design fidelity review: "raw render_episode
 * ... show as name and step").
 */
export function humanizeKind(kind: string): string {
  const words = kind.split(/[_-]+/).filter(Boolean);
  if (words.length === 0) return kind;
  return words.map((word, index) => (index === 0 ? word[0].toUpperCase() + word.slice(1) : word)).join(" ");
}
