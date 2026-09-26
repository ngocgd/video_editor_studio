import { Check, Columns2 } from "lucide-react";
import { useState } from "react";

import type { SceneTake } from "../../api/gen/types.gen";
import { assetUrl } from "./use-scenes";

/**
 * Takes of one kind for a scene (guidelines §7 regenerate/takes): numbered
 * thumbnails (images) or chips (voice/subtitles), the selected one marked;
 * clicking previews a take, "Use take" selects it (which is also how a
 * revert works), and A/B puts the selected take next to the previewed one.
 */
export function TakesStrip({
  takes,
  kind,
  onSelect,
  selecting = false,
}: {
  takes: SceneTake[];
  kind: SceneTake["kind"];
  onSelect: (takeId: string) => void;
  selecting?: boolean;
}) {
  const ofKind = takes.filter((t) => t.kind === kind);
  const selected = ofKind.find((t) => t.selected);
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [compare, setCompare] = useState(false);
  const preview = ofKind.find((t) => t.id === previewId) ?? selected ?? ofKind[ofKind.length - 1];

  if (ofKind.length === 0) {
    return <p className="text-xs text-muted-foreground">No takes yet.</p>;
  }

  const label = (t: SceneTake) => `T${ofKind.indexOf(t) + 1}`;
  const media = (t: SceneTake) =>
    kind === "image" ? (
      <img src={assetUrl(t.assetId, t.variants ? "webp-640" : "original")} alt={`Take ${label(t)}`} className="aspect-video w-full rounded-sm bg-well object-cover" />
    ) : kind === "voice" ? (
      <audio controls preload="none" src={assetUrl(t.assetId)} aria-label={`Voice take ${label(t)}`} className="h-8 w-full" />
    ) : (
      <a href={assetUrl(t.assetId)} className="text-xs text-primary-text underline" target="_blank" rel="noreferrer">
        Subtitle cues {label(t)}
      </a>
    );

  return (
    <div className="flex flex-col gap-2">
      <div role="radiogroup" aria-label={`${kind} takes`} className="flex flex-wrap items-center gap-1">
        {ofKind.map((t) => (
          <button
            key={t.id}
            type="button"
            role="radio"
            aria-checked={preview?.id === t.id}
            onClick={() => setPreviewId(t.id)}
            className={`flex h-7 items-center gap-1 rounded-sm border px-2 font-mono text-xs ${
              preview?.id === t.id ? "border-primary text-foreground" : "border-border text-text-2 hover:bg-accent"
            }`}
          >
            {label(t)}
            {t.selected && <Check size={12} aria-label="selected" />}
            {t.stale && <span className="text-warning">·stale</span>}
          </button>
        ))}
        <span className="flex-1" />
        {selected && preview && preview.id !== selected.id && (
          <button
            type="button"
            aria-pressed={compare}
            onClick={() => setCompare((v) => !v)}
            className="flex h-7 items-center gap-1 rounded-sm border border-border px-2 text-xs hover:bg-accent"
          >
            <Columns2 size={14} aria-hidden="true" /> A/B
          </button>
        )}
      </div>
      {compare && selected && preview && preview.id !== selected.id ? (
        <div className="grid grid-cols-2 gap-2" data-testid="takes-ab">
          <figure className="flex flex-col gap-1">
            {media(selected)}
            <figcaption className="text-2xs text-muted-foreground">A · {label(selected)} (selected)</figcaption>
          </figure>
          <figure className="flex flex-col gap-1">
            {media(preview)}
            <figcaption className="text-2xs text-muted-foreground">B · {label(preview)}</figcaption>
          </figure>
        </div>
      ) : (
        preview && media(preview)
      )}
      {preview && !preview.selected && (
        <button
          type="button"
          disabled={selecting}
          onClick={() => onSelect(preview.id)}
          className="self-start rounded-md border border-border bg-secondary px-2 py-1 text-xs hover:bg-accent disabled:opacity-50"
        >
          Use take {label(preview)}
        </button>
      )}
    </div>
  );
}
