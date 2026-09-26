import { RefreshCw, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import type { Character, ImageStyle, Scene, SceneSegment, TakeKind } from "../../api/gen/types.gen";
import { InspectorPanel, InspectorSection } from "../../components/shared/inspector-panel";
import { PipelinePips } from "../../components/shared/pipeline-pips";
import { formatTimecode } from "../../lib/format";
import { MOTION_LABELS, MOTION_PRESETS, toPipelinePips } from "./storyboard-model";
import { TakesStrip } from "./takes-strip";
import { assetUrl, useRegenerate, useSelectTake, useTakes, useUpdateScene } from "./use-scenes";

function charName(c: Character | undefined, lang: string): string {
  if (!c) return "Unknown";
  return (lang === "vi" ? c.names.vi || c.names.en : c.names.en || c.names.orig) || c.names.orig || c.names.vi;
}

const buttonClass = "flex h-7 items-center gap-1 rounded-md border border-border bg-secondary px-2 text-xs hover:bg-accent disabled:opacity-50";

/** Scene inspector (wireframe storyboard-scene-editor): image, narration, voice, subtitles and motion of the active scene. */
export function SceneInspector({
  episodeId,
  scene,
  characters,
  styles,
  editingNarration,
  onEditingNarrationChange,
  previewAssetId,
}: {
  episodeId: string;
  scene: Scene | undefined;
  characters: Character[];
  styles: ImageStyle[];
  editingNarration: boolean;
  onEditingNarrationChange: (editing: boolean) => void;
  /** The scene's 540p proxy from the latest render of this language, if any. */
  previewAssetId?: string;
}) {
  const update = useUpdateScene(episodeId);
  const regenerate = useRegenerate(episodeId);
  const selectTake = useSelectTake(episodeId);
  const takes = useTakes(scene?.id, scene?.version);
  const [draft, setDraft] = useState("");
  const [prompt, setPrompt] = useState("");
  const narrationRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    setDraft(scene?.narration ?? "");
    setPrompt(scene?.imagePrompt ?? "");
  }, [scene?.id, scene?.version, scene?.narration, scene?.imagePrompt]);

  useEffect(() => {
    if (editingNarration) narrationRef.current?.focus();
  }, [editingNarration]);

  if (!scene) {
    return (
      <InspectorPanel title="Scene">
        <p className="text-sm text-text-2">Select a scene.</p>
      </InspectorPanel>
    );
  }

  const byId = new Map(characters.map((c) => [c.id, c]));
  const pip = (kind: string) => scene.pips.find((p) => p.kind === kind);
  const patch = (body: Omit<Parameters<typeof update.mutate>[0]["body"], "expectedVersion">) =>
    update.mutate({ id: scene.id, body: { ...body, expectedVersion: scene.version } });
  const regen = (kind: TakeKind) => regenerate.mutate({ id: scene.id, kind });
  const voiceStale = pip("voice")?.state === "stale" ? pip("voice")?.staleReason : undefined;
  const assignSpeaker = (index: number, characterId: string) => {
    const segments: SceneSegment[] = scene.segments.map((s, i) =>
      i === index ? { text: s.text, speakerCharacterId: characterId || undefined } : s,
    );
    patch({ segments });
  };

  return (
    <InspectorPanel title={`Scene ${String(scene.idx).padStart(3, "0")}`}>
      <div className="flex flex-col gap-1">
        <span className="font-mono text-xs tabular-nums text-muted-foreground">
          {formatTimecode(scene.startMs / 1000)} → {formatTimecode((scene.startMs + scene.durationMs) / 1000)} · {formatTimecode(scene.durationMs / 1000)}
          {scene.durationMeasured ? "" : " (estimated)"}
        </span>
        <PipelinePips pips={toPipelinePips(scene.pips)} />
        {scene.tainted && <span className="text-2xs text-warning">Imported text: review before publishing.</span>}
      </div>

      <InspectorSection title="Image">
        <TakesStrip takes={takes.data?.items ?? []} kind="image" selecting={selectTake.isPending} onSelect={(takeId) => selectTake.mutate({ sceneId: scene.id, takeId })} />
        <button type="button" className={buttonClass} onClick={() => regen("image")} disabled={regenerate.isPending}>
          <RefreshCw size={14} aria-hidden="true" /> Regenerate image <kbd className="font-mono text-[10px] text-muted-foreground">I</kbd>
        </button>
        <label className="flex flex-col gap-1 text-xs text-text-2">
          Image prompt
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onBlur={() => prompt !== scene.imagePrompt && patch({ imagePrompt: prompt })}
            rows={3}
            className="rounded-md border border-input bg-well p-2 text-sm text-foreground"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-text-2">
          Style
          <select
            value={scene.imageStyleId ?? ""}
            onChange={(e) => patch(e.target.value ? { imageStyleId: e.target.value } : { clearImageStyle: true })}
            className="h-8 rounded-md border border-input bg-well px-2 text-sm text-foreground"
          >
            <option value="">Series default</option>
            {styles.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        <fieldset className="flex flex-col gap-1 text-xs text-text-2">
          <legend>Characters in the image</legend>
          {characters.length === 0 && <span className="text-muted-foreground">No characters in this series yet.</span>}
          {characters.map((c) => (
            <label key={c.id} className="flex items-center gap-2 text-sm text-foreground">
              <input
                type="checkbox"
                checked={scene.characterIds.includes(c.id)}
                onChange={(e) =>
                  patch({ characterIds: e.target.checked ? [...scene.characterIds, c.id] : scene.characterIds.filter((id) => id !== c.id) })
                }
              />
              {charName(c, scene.lang)}
            </label>
          ))}
        </fieldset>
      </InspectorSection>

      <InspectorSection title="Narration">
        {voiceStale && (
          <p role="status" className="flex items-start gap-1 rounded-md bg-warning-muted p-2 text-xs text-warning">
            <TriangleAlert size={14} aria-hidden="true" className="mt-0.5 shrink-0" />
            {voiceStale}. Voice and subtitles are out of date for this scene only.
          </p>
        )}
        {editingNarration ? (
          <div className="flex flex-col gap-2">
            <textarea
              ref={narrationRef}
              aria-label="Narration"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              rows={8}
              className="rounded-md border border-input bg-well p-2 font-serif text-sm text-foreground"
            />
            <div className="flex gap-2">
              <button
                type="button"
                className={buttonClass}
                disabled={update.isPending || draft.trim() === ""}
                onClick={() => {
                  patch({ narration: draft });
                  onEditingNarrationChange(false);
                }}
              >
                Save narration
              </button>
              <button type="button" className={buttonClass} onClick={() => onEditingNarrationChange(false)}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            {scene.segments.map((seg, i) => (
              <div key={i} className="flex flex-col gap-0.5">
                <span className="text-2xs font-medium uppercase tracking-[0.04em] text-muted-foreground">
                  {seg.speakerCharacterId ? charName(byId.get(seg.speakerCharacterId), scene.lang) : "Narrator"}
                  {seg.unrecognisedName && <span className="ml-1 normal-case text-warning">(speaker not recognised: {seg.unrecognisedName})</span>}
                </span>
                <p className="font-serif text-sm text-foreground">{seg.text}</p>
                {seg.unrecognisedName && (
                  <select
                    aria-label={`Assign speaker for “${seg.unrecognisedName}”`}
                    defaultValue=""
                    onChange={(e) => assignSpeaker(i, e.target.value)}
                    className="h-7 self-start rounded-md border border-input bg-well px-2 text-xs"
                  >
                    <option value="">Keep narrator</option>
                    {characters.map((c) => (
                      <option key={c.id} value={c.id}>
                        {charName(c, scene.lang)}
                      </option>
                    ))}
                  </select>
                )}
              </div>
            ))}
            <button type="button" className={`${buttonClass} self-start`} onClick={() => onEditingNarrationChange(true)}>
              Edit <kbd className="font-mono text-[10px] text-muted-foreground">E</kbd>
            </button>
          </div>
        )}
      </InspectorSection>

      <InspectorSection title="Voice">
        <TakesStrip takes={takes.data?.items ?? []} kind="voice" selecting={selectTake.isPending} onSelect={(takeId) => selectTake.mutate({ sceneId: scene.id, takeId })} />
        <button type="button" className={buttonClass} onClick={() => regen("voice")} disabled={regenerate.isPending}>
          <RefreshCw size={14} aria-hidden="true" /> Regenerate voice <kbd className="font-mono text-[10px] text-muted-foreground">V</kbd>
        </button>
      </InspectorSection>

      <InspectorSection title="Subtitles">
        <TakesStrip takes={takes.data?.items ?? []} kind="align" selecting={selectTake.isPending} onSelect={(takeId) => selectTake.mutate({ sceneId: scene.id, takeId })} />
        <button type="button" className={buttonClass} onClick={() => regen("align")} disabled={regenerate.isPending || !scene.voiceAssetId}>
          <RefreshCw size={14} aria-hidden="true" /> Re-align subtitles
        </button>
      </InspectorSection>

      <InspectorSection title="Motion">
        <div role="radiogroup" aria-label="Motion preset" className="flex gap-1">
          {MOTION_PRESETS.map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={scene.motionPreset === m}
              onClick={() => patch({ motionPreset: m })}
              className={`h-7 rounded-md border px-2 text-xs ${scene.motionPreset === m ? "border-primary bg-primary-muted" : "border-border hover:bg-accent"}`}
            >
              {MOTION_LABELS[m]}
            </button>
          ))}
        </div>
        <span className="text-2xs text-muted-foreground">
          Press <kbd className="font-mono">M</kbd> to cycle. FFmpeg motion, no GPU slot needed.
        </span>
      </InspectorSection>
      <InspectorSection title="Render preview">
        {previewAssetId ? (
          <>
            <video key={previewAssetId} src={assetUrl(previewAssetId)} controls preload="metadata" className="aspect-video w-full rounded-md bg-black" aria-label={`Scene ${scene.idx} render preview`} />
            <span className="text-2xs text-muted-foreground">From the latest render; edits since then show after the next render.</span>
          </>
        ) : (
          <span className="text-xs text-text-2">No render of this scene yet.</span>
        )}
      </InspectorSection>
      {(update.error || regenerate.error) && (
        <p role="alert" className="text-xs text-destructive">
          {(update.error ?? regenerate.error)?.message}
        </p>
      )}
    </InspectorPanel>
  );
}
