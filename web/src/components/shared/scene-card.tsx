import { formatTimecode } from "../../lib/format";
import { type PipelinePip, PipelinePips } from "./pipeline-pips";
import { type Speaker, SpeakerMonogram } from "./speaker-monogram";

export interface SceneImageVariant {
  src: string;
  type: "image/avif" | "image/webp" | "image/jpeg";
  width: number;
}

/**
 * Storyboard scene tile (guidelines §7): 16:9 thumbnail, index + mono
 * timecode, 2-line clamped narration, pipeline pips, speaker monogram.
 * `<picture>` with AVIF/WebP srcset from asset variants (phase 6/7 wire the
 * variant URLs); `width`/`height` are fixed to avoid CLS while the image is
 * lazy-loaded.
 */
export function SceneCard({
  index,
  timecodeS,
  narration,
  variants,
  fallbackSrc,
  pips,
  speaker,
  selected = false,
  onSelect,
}: {
  index: number;
  timecodeS: number;
  narration: string;
  variants: SceneImageVariant[];
  fallbackSrc: string;
  pips: PipelinePip[];
  speaker?: Speaker;
  selected?: boolean;
  onSelect?: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-selected={selected}
      className={
        selected
          ? "flex flex-col gap-1.5 rounded-md p-1.5 text-left outline outline-2 outline-primary"
          : "flex flex-col gap-1.5 rounded-md p-1.5 text-left hover:bg-card"
      }
    >
      <div className="relative aspect-video w-full overflow-hidden rounded-sm bg-well">
        <picture>
          {variants.map((variant) => (
            <source key={variant.type} srcSet={variant.src} type={variant.type} />
          ))}
          <img
            src={fallbackSrc}
            alt=""
            loading="lazy"
            width={320}
            height={180}
            className="h-full w-full object-cover"
          />
        </picture>
        {speaker && <SpeakerMonogram speaker={speaker} className="absolute bottom-1 right-1" />}
      </div>
      <div className="flex items-center gap-2 text-xs text-text-2">
        <span className="font-mono tabular-nums">#{String(index).padStart(3, "0")}</span>
        <span className="font-mono tabular-nums">{formatTimecode(timecodeS)}</span>
      </div>
      <p className="line-clamp-2 text-sm text-foreground">{narration}</p>
      <PipelinePips pips={pips} />
    </button>
  );
}
