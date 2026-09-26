import type { MouseEvent } from "react";

import { formatTimecode } from "../../lib/format";
import { type PipelinePip, PipelinePips } from "./pipeline-pips";
import { type Speaker, SpeakerMonogram } from "./speaker-monogram";

export interface SceneImageVariant {
  /** A `srcset` value, e.g. "a.avif 320w, b.avif 640w". */
  srcSet: string;
  type: "image/avif" | "image/webp" | "image/jpeg";
}

/**
 * Storyboard scene tile (guidelines §7): 16:9 thumbnail, index + mono
 * timecode, 2-line clamped narration, pipeline pips, speaker monogram.
 * `<picture>` with AVIF/WebP srcset from asset variants; `width`/`height`
 * are fixed so a lazily loaded image never shifts the layout.
 */
export function SceneCard({
  index,
  timecodeS,
  durationS,
  narration,
  variants,
  fallbackSrc,
  sizes,
  placeholder,
  pips,
  pipLabel,
  speakers = [],
  selected = false,
  active = false,
  onSelect,
}: {
  index: number;
  timecodeS: number;
  durationS?: number;
  narration: string;
  variants: SceneImageVariant[];
  fallbackSrc?: string;
  sizes?: string;
  /** Shown in the thumbnail box when there is no image yet. */
  placeholder?: string;
  pips: PipelinePip[];
  /** Screen-reader summary of the pips. */
  pipLabel?: string;
  speakers?: Speaker[];
  selected?: boolean;
  active?: boolean;
  onSelect?: (event: MouseEvent<HTMLButtonElement>) => void;
}) {
  const ring = active ? "outline outline-2 outline-primary" : selected ? "outline outline-1 outline-primary/60" : "hover:bg-card";
  return (
    <button
      type="button"
      tabIndex={-1}
      onClick={onSelect}
      aria-pressed={selected}
      aria-label={`Scene ${index}, ${formatTimecode(timecodeS)}${pipLabel ? `. ${pipLabel}` : ""}`}
      className={`flex h-full w-full flex-col gap-1.5 rounded-md p-1.5 text-left ${ring}`}
    >
      <div className="relative aspect-video w-full overflow-hidden rounded-sm bg-well">
        {fallbackSrc ? (
          <picture>
            {variants.map((variant) => (
              <source key={variant.type} srcSet={variant.srcSet} sizes={sizes} type={variant.type} />
            ))}
            <img src={fallbackSrc} alt="" loading="lazy" decoding="async" width={320} height={180} className="h-full w-full object-cover" />
          </picture>
        ) : (
          <span className="flex h-full w-full items-center justify-center text-xs text-muted-foreground">{placeholder ?? "No image yet"}</span>
        )}
        <span className="absolute left-1 top-1 rounded-sm bg-well/80 px-1 font-mono text-[10px] tabular-nums text-text-2">{formatTimecode(timecodeS)}</span>
        {durationS != null && (
          <span className="absolute right-1 top-1 rounded-sm bg-well/80 px-1 font-mono text-[10px] tabular-nums text-text-2">{formatTimecode(durationS)}</span>
        )}
      </div>
      <div className="flex items-center gap-1 text-xs text-text-2">
        <span className="font-mono tabular-nums">#{String(index).padStart(3, "0")}</span>
        <span className="flex-1" />
        {speakers.slice(0, 3).map((speaker) => (
          <SpeakerMonogram key={speaker.id} speaker={speaker} />
        ))}
      </div>
      <p className="line-clamp-2 text-sm text-foreground">{narration}</p>
      <PipelinePips pips={pips} />
    </button>
  );
}
