import { useQueries } from "@tanstack/react-query";
import { Minus, Pause, Play, Plus, SkipBack, SkipForward } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { getScenePeaksOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { Peaks } from "../../api/gen/types.gen";
import { formatTimecode } from "../../lib/format";
import {
  clampViewStart,
  type Clip,
  rulerStepMs,
  visibleClips,
  WAVEFORM_MAX_VISIBLE_MS,
  zoomAround,
} from "./timeline-math";

export interface TimelineClip extends Clip {
  idx: number;
  label: string;
  state: "done" | "stale" | "failed" | "missing";
  hasPeaks: boolean;
}

const RULER_H = 18;
const VIDEO_H = 34;
const AUDIO_H = 34;
const SUB_H = 18;
const HEIGHT = RULER_H + VIDEO_H + AUDIO_H + SUB_H + 6;

/** Canvas colours read once from the design tokens. */
function palette() {
  const css = typeof window !== "undefined" ? getComputedStyle(document.documentElement) : null;
  const v = (name: string, fallback: string) => css?.getPropertyValue(name).trim() || fallback;
  return {
    bg: v("--well", "#0e0f11"),
    clip: v("--card", "#1b1c20"),
    border: v("--border", "#34363c"),
    text: v("--text-2", "#a8a6a0"),
    primary: v("--primary", "#4db6a0"),
    warn: v("--warning", "#d9a441"),
    err: v("--destructive", "#ee6b61"),
    wave: v("--info", "#6fa8dc"),
    playhead: v("--foreground", "#e6e4df"),
  };
}

/**
 * The episode timeline (guidelines §7): V1 image clips sized by duration,
 * A1 waveform drawn on a canvas from peaks fetched only for the visible
 * window, S1 subtitle lane, a ruler and a playhead. Wheel pans, Ctrl+wheel
 * zooms around the pointer; drawing is one requestAnimationFrame per
 * change and only visits clips in view (binary search), so a 3-hour
 * episode of ~400 clips pans and zooms without layout work.
 */
export function Timeline({
  clips,
  totalMs,
  activeId,
  playheadMs,
  playing,
  onSelect,
  onPlayToggle,
  onPrev,
  onNext,
}: {
  clips: TimelineClip[];
  totalMs: number;
  activeId?: string;
  playheadMs?: number;
  playing: boolean;
  onSelect: (id: string) => void;
  onPlayToggle: () => void;
  onPrev: () => void;
  onNext: () => void;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [width, setWidth] = useState(800);
  const [pxPerS, setPxPerS] = useState(4);
  const [viewStartMs, setViewStartMs] = useState(0);
  const colors = useMemo(palette, []);

  useLayoutEffect(() => {
    const el = boxRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => setWidth(Math.max(100, Math.floor(entries[0].contentRect.width))));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const visibleMs = (width / pxPerS) * 1000;
  const start = clampViewStart(viewStartMs, visibleMs, totalMs);
  const { from, to } = visibleClips(clips, start, start + visibleMs);

  // Follow the active clip: scroll it into view when selection moves.
  useEffect(() => {
    const clip = clips.find((c) => c.id === activeId);
    if (!clip) return;
    if (clip.startMs < start || clip.startMs + clip.durationMs > start + visibleMs) {
      setViewStartMs(Math.max(0, clip.startMs - visibleMs * 0.2));
    }
  }, [activeId]); // eslint-disable-line react-hooks/exhaustive-deps

  const waveformScenes = visibleMs <= WAVEFORM_MAX_VISIBLE_MS ? clips.slice(from, to).filter((c) => c.hasPeaks) : [];
  const peaks = useQueries({
    queries: waveformScenes.map((c) => ({ ...getScenePeaksOptions({ path: { id: c.id } }), staleTime: Infinity })),
  });
  const peaksById = new Map<string, Peaks>();
  waveformScenes.forEach((c, i) => {
    const data = peaks[i]?.data;
    if (data) peaksById.set(c.id, data);
  });
  const peaksKey = [...peaksById.keys()].join(",");

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const frame = requestAnimationFrame(() => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = width * dpr;
      canvas.height = HEIGHT * dpr;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.fillStyle = colors.bg;
      ctx.fillRect(0, 0, width, HEIGHT);
      const x = (ms: number) => ((ms - start) / 1000) * pxPerS;

      // Ruler.
      ctx.font = "10px 'IBM Plex Mono', monospace";
      ctx.fillStyle = colors.text;
      ctx.strokeStyle = colors.border;
      const step = rulerStepMs(pxPerS);
      for (let t = Math.floor(start / step) * step; t < start + visibleMs; t += step) {
        const px = Math.round(x(t)) + 0.5;
        ctx.beginPath();
        ctx.moveTo(px, 0);
        ctx.lineTo(px, RULER_H);
        ctx.stroke();
        ctx.fillText(formatTimecode(t / 1000), px + 3, 11);
      }

      const vY = RULER_H + 2;
      const aY = vY + VIDEO_H + 2;
      const sY = aY + AUDIO_H + 2;
      for (let i = from; i < to; i += 1) {
        const c = clips[i];
        const x0 = x(c.startMs);
        const w = Math.max(1, (c.durationMs / 1000) * pxPerS - 1);
        ctx.fillStyle = c.state === "missing" ? colors.bg : colors.clip;
        ctx.fillRect(x0, vY, w, VIDEO_H);
        ctx.strokeStyle = c.id === activeId ? colors.primary : c.state === "stale" ? colors.warn : c.state === "failed" ? colors.err : colors.border;
        ctx.lineWidth = c.id === activeId ? 2 : 1;
        ctx.strokeRect(x0 + 0.5, vY + 0.5, w - 1, VIDEO_H - 1);
        if (w > 28) {
          ctx.fillStyle = colors.text;
          ctx.fillText(`#${String(c.idx).padStart(3, "0")}`, x0 + 4, vY + 13);
        }
        // Waveform (A1) from the scene's peaks, one min/max pair per pixel.
        const p = peaksById.get(c.id);
        if (p && p.min.length > 0) {
          ctx.fillStyle = colors.wave;
          const mid = aY + AUDIO_H / 2;
          const perPx = p.peaksPerSecond / pxPerS;
          const pxFrom = Math.max(0, Math.floor(-x0));
          const pxTo = Math.min(Math.ceil(w), width - Math.floor(x0));
          for (let px = pxFrom; px < pxTo; px += 1) {
            const a = Math.floor(px * perPx);
            const b = Math.min(p.min.length, Math.max(a + 1, Math.floor((px + 1) * perPx)));
            let lo = 0;
            let hi = 0;
            for (let k = a; k < b; k += 1) {
              lo = Math.min(lo, p.min[k]);
              hi = Math.max(hi, p.max[k]);
            }
            const top = mid - (hi / 127) * (AUDIO_H / 2);
            ctx.fillRect(x0 + px, top, 1, Math.max(1, ((hi - lo) / 127) * (AUDIO_H / 2)));
          }
        }
        // Subtitles (S1): the scene's text, clipped to its span.
        if (w > 40) {
          ctx.save();
          ctx.beginPath();
          ctx.rect(x0, sY, w - 2, SUB_H);
          ctx.clip();
          ctx.fillStyle = colors.text;
          ctx.fillText(c.label, x0 + 3, sY + 12);
          ctx.restore();
        }
      }
      if (playheadMs != null && playheadMs >= start && playheadMs <= start + visibleMs) {
        ctx.fillStyle = colors.playhead;
        ctx.fillRect(Math.round(x(playheadMs)), 0, 1, HEIGHT);
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [clips, from, to, start, visibleMs, pxPerS, width, activeId, playheadMs, peaksKey, colors]); // eslint-disable-line react-hooks/exhaustive-deps

  // Registered natively (not as a React prop) so it can be non-passive:
  // Ctrl+wheel must not also zoom the whole page.
  const wheelRef = useRef<(event: WheelEvent) => void>(() => {});
  wheelRef.current = (event: WheelEvent) => {
    event.preventDefault();
    if (event.ctrlKey) {
      const rect = canvasRef.current?.getBoundingClientRect();
      const next = zoomAround(start, pxPerS, event.deltaY < 0 ? 1.25 : 0.8, event.clientX - (rect?.left ?? 0));
      setPxPerS(next.pxPerS);
      setViewStartMs(next.viewStartMs);
    } else {
      const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
      setViewStartMs(clampViewStart(start + (delta / pxPerS) * 1000, visibleMs, totalMs));
    }
  };
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const listener = (event: WheelEvent) => wheelRef.current(event);
    canvas.addEventListener("wheel", listener, { passive: false });
    return () => canvas.removeEventListener("wheel", listener);
  }, []);

  function onClick(event: React.MouseEvent<HTMLCanvasElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    const ms = start + ((event.clientX - rect.left) / pxPerS) * 1000;
    const hit = clips.slice(from, to).find((c) => ms >= c.startMs && ms < c.startMs + c.durationMs);
    if (hit) onSelect(hit.id);
  }

  const zoom = (factor: number) => {
    const next = zoomAround(start, pxPerS, factor, width / 2);
    setPxPerS(next.pxPerS);
    setViewStartMs(next.viewStartMs);
  };
  const iconButton = "rounded-md p-1 text-text-2 hover:bg-accent hover:text-foreground";

  return (
    <section aria-label="Episode timeline" className="flex flex-col gap-1 border-t border-border pt-2">
      <div className="flex items-center gap-2 text-xs">
        <button type="button" aria-label="Previous scene (J)" onClick={onPrev} className={iconButton}>
          <SkipBack size={16} aria-hidden="true" />
        </button>
        <button type="button" aria-label={playing ? "Pause (Space)" : "Play scene audio (Space)"} onClick={onPlayToggle} className={iconButton}>
          {playing ? <Pause size={16} aria-hidden="true" /> : <Play size={16} aria-hidden="true" />}
        </button>
        <button type="button" aria-label="Next scene (K)" onClick={onNext} className={iconButton}>
          <SkipForward size={16} aria-hidden="true" />
        </button>
        <span className="font-mono tabular-nums text-foreground">{formatTimecode((playheadMs ?? 0) / 1000)}</span>
        <span className="font-mono tabular-nums text-muted-foreground">/ {formatTimecode(totalMs / 1000)}</span>
        <span className="flex-1" />
        <span className="text-muted-foreground">
          Showing {formatTimecode(start / 1000)} – {formatTimecode((start + visibleMs) / 1000)}
        </span>
        <button type="button" aria-label="Zoom out" onClick={() => zoom(0.5)} className={iconButton}>
          <Minus size={16} aria-hidden="true" />
        </button>
        <button type="button" aria-label="Zoom in" onClick={() => zoom(2)} className={iconButton}>
          <Plus size={16} aria-hidden="true" />
        </button>
      </div>
      <div ref={boxRef} className="w-full">
        <canvas
          ref={canvasRef}
          data-testid="timeline-canvas"
          role="img"
          aria-label={`Timeline, ${clips.length} clips, ${formatTimecode(totalMs / 1000)} total`}
          style={{ width, height: HEIGHT }}
          onClick={onClick}
          className="block cursor-pointer"
        />
      </div>
    </section>
  );
}
