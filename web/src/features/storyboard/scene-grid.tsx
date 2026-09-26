import { type MouseEvent, useLayoutEffect, useRef, useState } from "react";

import type { Scene } from "../../api/gen/types.gen";
import { SceneCard } from "../../components/shared/scene-card";
import type { Speaker } from "../../components/shared/speaker-monogram";
import { VirtualGrid } from "../../components/shared/virtual-grid";
import { columnsForWidth, GRID_GAP, pipSummary, TILE_HEIGHT, toPipelinePips } from "./storyboard-model";
import { assetUrl } from "./use-scenes";

function srcSet(assetId: string, format: "avif" | "webp"): string {
  return [320, 640, 1280].map((w) => `${assetUrl(assetId, `${format}-${w as 320 | 640 | 1280}`)} ${w}w`).join(", ");
}

function placeholderFor(scene: Scene): string {
  const image = scene.pips.find((p) => p.kind === "image");
  if (image?.state === "queued") return "Queued for GPU";
  if (image?.state === "running") return "Generating…";
  if (image?.state === "failed") return "Image failed";
  return "No image yet";
}

/**
 * The virtualized scene grid: columns come from the container width
 * (ResizeObserver), tile height is fixed, and only visible rows mount.
 */
export function SceneGrid({
  scenes,
  activeIndex,
  selectedIds,
  speakersById,
  onActivate,
  onTileClick,
}: {
  scenes: Scene[];
  activeIndex: number;
  selectedIds: Set<string>;
  speakersById: Map<string, Speaker>;
  onActivate: (index: number) => void;
  onTileClick: (index: number, event: MouseEvent<HTMLButtonElement>) => void;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(960);

  useLayoutEffect(() => {
    const el = boxRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.floor(entries[0].contentRect.width);
      setWidth((prev) => (prev === next ? prev : next));
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const columns = columnsForWidth(width);
  const tileWidth = Math.floor((width - GRID_GAP * (columns - 1)) / columns);
  const sizes = `${tileWidth}px`;

  return (
    <div ref={boxRef} className="min-h-0 flex-1">
      <VirtualGrid
        items={scenes}
        columns={columns}
        rowHeight={TILE_HEIGHT}
        gap={GRID_GAP}
        getItemId={(s) => s.id}
        activeIndex={activeIndex}
        onActiveIndexChange={onActivate}
        ariaLabel="Scenes"
        className="h-full"
        renderItem={(scene, index, isActive) => {
          const speakers: Speaker[] = [];
          for (const seg of scene.segments) {
            const sp = seg.speakerCharacterId ? speakersById.get(seg.speakerCharacterId) : speakersById.get("narrator");
            if (sp && !speakers.some((s) => s.id === sp.id)) speakers.push(sp);
          }
          return (
            <SceneCard
              index={scene.idx}
              timecodeS={scene.startMs / 1000}
              durationS={scene.durationMs / 1000}
              narration={scene.narration}
              variants={scene.imageAssetId && scene.imageVariants ? [
                { srcSet: srcSet(scene.imageAssetId, "avif"), type: "image/avif" },
                { srcSet: srcSet(scene.imageAssetId, "webp"), type: "image/webp" },
              ] : []}
              fallbackSrc={scene.imageAssetId ? assetUrl(scene.imageAssetId, scene.imageVariants ? "webp-320" : "original") : undefined}
              sizes={sizes}
              placeholder={placeholderFor(scene)}
              pips={toPipelinePips(scene.pips)}
              pipLabel={pipSummary(scene.pips)}
              speakers={speakers}
              active={isActive}
              selected={selectedIds.has(scene.id)}
              onSelect={(event) => onTileClick(index, event)}
            />
          );
        }}
      />
    </div>
  );
}
