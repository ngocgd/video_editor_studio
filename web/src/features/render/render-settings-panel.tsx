import { useEffect, useState } from "react";

import type { RenderEstimate, RenderSettings, SceneLanguage } from "../../api/gen/types.gen";
import { InspectorPanel } from "../../components/shared/inspector-panel";
import { Button } from "../../components/ui/button";
import { encoderLabel, formatClock, formatRoughMinutes, OUTPUT_SIZES } from "./render-model";

const fieldClass = "h-8 w-full rounded-md border border-input bg-well px-2 text-sm";
const labelClass = "flex flex-col gap-1 text-xs font-medium text-text-2";

function Segmented<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: { value: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div className={labelClass}>
      <span>{label}</span>
      <div role="radiogroup" aria-label={label} className="flex overflow-hidden rounded-md border border-border">
        {options.map((o) => (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={value === o.value}
            onClick={() => onChange(o.value)}
            className={`h-8 flex-1 border-r border-border px-2 text-sm last:border-r-0 ${value === o.value ? "bg-primary-muted text-foreground" : "text-text-2 hover:bg-accent"}`}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

/**
 * Right-hand render settings (wireframe render-queue): output, language,
 * subtitles and their style, default motion, transition, loudness, the
 * "no background music" statement and the estimate.
 */
export function RenderSettingsPanel({
  episodeTitle,
  settings,
  langs,
  lang,
  onLangChange,
  estimate,
  saving,
  saveError,
  onSave,
}: {
  episodeTitle: string;
  settings: RenderSettings | undefined;
  langs: SceneLanguage[];
  lang: SceneLanguage;
  onLangChange: (lang: SceneLanguage) => void;
  estimate: RenderEstimate | undefined;
  saving: boolean;
  saveError?: string;
  onSave: (settings: RenderSettings) => void;
}) {
  const [form, setForm] = useState<RenderSettings | undefined>(settings);
  useEffect(() => setForm(settings), [settings]);
  const set = <K extends keyof RenderSettings>(key: K, value: RenderSettings[K]) => setForm((f) => (f ? { ...f, [key]: value } : f));
  const dirty = JSON.stringify(form) !== JSON.stringify(settings);
  const size = form ? `${form.width}x${form.height}` : "";
  const knownSize = OUTPUT_SIZES.some((s) => `${s.width}x${s.height}` === size);

  return (
    <InspectorPanel title={`Render settings · ${episodeTitle}`}>
      {!form ? (
        <p className="text-sm text-text-2">Loading settings…</p>
      ) : (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            onSave(form);
          }}
        >
          <div className="grid grid-cols-[1fr_auto_auto] gap-2">
            <label className={labelClass}>
              Output
              <select
                className={fieldClass}
                value={size}
                onChange={(e) => {
                  const [w, h] = e.target.value.split("x").map(Number);
                  setForm((f) => (f ? { ...f, width: w, height: h } : f));
                }}
              >
                {!knownSize && <option value={size}>{size.replace("x", "×")}</option>}
                {OUTPUT_SIZES.map((s) => (
                  <option key={s.label} value={`${s.width}x${s.height}`}>
                    {s.width}×{s.height}
                  </option>
                ))}
              </select>
            </label>
            <label className={labelClass}>
              FPS
              <select className={fieldClass} value={form.fps} onChange={(e) => set("fps", Number(e.target.value) as RenderSettings["fps"])}>
                {[24, 25, 30, 60].map((f) => (
                  <option key={f} value={f}>
                    {f}
                  </option>
                ))}
              </select>
            </label>
            <label className={labelClass}>
              Encoder
              <select className={fieldClass} value={form.encoder} onChange={(e) => set("encoder", e.target.value)}>
                {["auto", "h264_nvenc", "libx264"].map((enc) => (
                  <option key={enc} value={enc}>
                    {encoderLabel(enc)}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <Segmented label="Language" value={lang} options={langs.map((l) => ({ value: l, label: l.toUpperCase() }))} onChange={onLangChange} />
          <Segmented
            label="Subtitles"
            value={form.subtitles}
            options={[
              { value: "burn", label: "Burn-in" },
              { value: "srt", label: "SRT only" },
              { value: "both", label: "Both" },
            ]}
            onChange={(v) => set("subtitles", v)}
          />

          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1 text-xs font-medium text-text-2">Subtitle style</legend>
            <div className="grid grid-cols-2 gap-2">
              <label className={labelClass}>
                Position
                <select className={fieldClass} value={form.subtitleStyle.position} onChange={(e) => set("subtitleStyle", { ...form.subtitleStyle, position: e.target.value as RenderSettings["subtitleStyle"]["position"] })}>
                  <option value="bottom">Bottom</option>
                  <option value="middle">Middle</option>
                  <option value="top">Top</option>
                </select>
              </label>
              <label className={labelClass}>
                Font
                <input className={fieldClass} value={form.subtitleStyle.font} maxLength={64} onChange={(e) => set("subtitleStyle", { ...form.subtitleStyle, font: e.target.value })} />
              </label>
              <label className={labelClass}>
                Size (px)
                <input type="number" min={12} max={160} className={fieldClass} value={form.subtitleStyle.sizePx} onChange={(e) => set("subtitleStyle", { ...form.subtitleStyle, sizePx: Number(e.target.value) })} />
              </label>
              <label className={labelClass}>
                Shadow (px)
                <input type="number" min={0} max={10} className={fieldClass} value={form.subtitleStyle.shadowPx} onChange={(e) => set("subtitleStyle", { ...form.subtitleStyle, shadowPx: Number(e.target.value) })} />
              </label>
            </div>
          </fieldset>

          <label className={labelClass}>
            Default motion
            <select className={fieldClass} value={form.defaultMotion} onChange={(e) => set("defaultMotion", e.target.value as RenderSettings["defaultMotion"])}>
              <option value="ken_burns">Ken Burns · slow push-in</option>
              <option value="static">Static</option>
              {/* Parallax needs a depth model the GPU worker does not offer yet. */}
              <option value="parallax" disabled>
                Parallax (depth model not installed)
              </option>
            </select>
          </label>
          <div className="grid grid-cols-3 gap-2">
            <label className={labelClass}>
              Crossfade (ms)
              <input type="number" min={0} max={3000} step={100} className={fieldClass} value={form.crossfadeMs} onChange={(e) => set("crossfadeMs", Number(e.target.value))} />
            </label>
            <label className={labelClass}>
              Loudness (LUFS)
              <input type="number" min={-30} max={-5} step={0.5} className={fieldClass} value={form.loudnessLufs} onChange={(e) => set("loudnessLufs", Number(e.target.value))} />
            </label>
            <label className={labelClass}>
              True peak (dBTP)
              <input type="number" min={-9} max={0} step={0.5} className={fieldClass} value={form.truePeakDbtp} onChange={(e) => set("truePeakDbtp", Number(e.target.value))} />
            </label>
          </div>
          <p className="border-l-2 border-border pl-2 text-xs text-text-2">No background music: narration only, by project decision.</p>
          {saveError && (
            <p role="alert" className="text-xs text-destructive">
              {saveError}
            </p>
          )}
          <Button type="submit" variant="secondary" size="sm" disabled={!dirty || saving}>
            {saving ? "Saving…" : "Save settings"}
          </Button>
        </form>
      )}

      <section aria-label="Estimate" className="mt-4 flex flex-col gap-1.5 border-t border-border pt-3 text-sm">
        <h3 className="text-2xs font-medium uppercase tracking-[0.04em] text-text-2">Estimate</h3>
        {estimate ? (
          <dl className="grid grid-cols-[1fr_auto] gap-y-1.5">
            <dt className="text-text-2">Episode length</dt>
            <dd className="font-mono tabular-nums">{formatClock(estimate.durationMs)}</dd>
            <dt className="text-text-2">Segments</dt>
            <dd className="font-mono tabular-nums">
              {estimate.segments} · {estimate.cachedSegments} cached
            </dd>
            <dt className="text-text-2">Encode ({encoderLabel(estimate.encoder)})</dt>
            <dd className="font-mono tabular-nums">{formatRoughMinutes(estimate.encodeSeconds)}</dd>
          </dl>
        ) : (
          <p className="text-xs text-text-2">Available once every scene has an image, a voice and subtitles.</p>
        )}
      </section>
    </InspectorPanel>
  );
}
