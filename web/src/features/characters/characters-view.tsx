import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, Plus, RefreshCw, Upload } from "lucide-react";
import { useEffect, useState } from "react";

import { getSeriesOptions, listVoicePresetsOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { Character, CharacterInput, CharacterVoice, VoiceLanguage, VoicePreset } from "../../api/gen/types.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { StatusChip } from "../../components/shared/status-chip";
import { TTS_ENGINES } from "../presets/voice-presets-view";
import { useMediaUpload } from "../presets/use-media-upload";
import { assetUrl } from "../storyboard/use-scenes";
import { useCharacterActions, useCharacters, useRunProgress } from "./use-characters";

const inputClass = "h-8 rounded-md border border-input bg-well px-2 text-sm";
const buttonClass = "flex h-7 items-center gap-1 rounded-md border border-border bg-secondary px-2 text-xs hover:bg-accent disabled:opacity-50";
const LANGS: VoiceLanguage[] = ["en", "vi"];

/** Rough client-side mirror of the server's token estimate (4 chars per token). */
export function estimateTokens(text: string): number {
  return Math.ceil(new TextEncoder().encode(text).length / 4);
}

function displayName(c: Character): string {
  return c.names.en || c.names.orig || c.names.vi;
}

function emptyInput(): CharacterInput {
  return { names: { orig: "", en: "", vi: "" }, role: "", appearancePrompt: "", negativePrompt: "", triggerToken: "", profile: "", pinned: true };
}

function toInput(c: Character): CharacterInput {
  return {
    names: c.names,
    role: c.role,
    appearancePrompt: c.appearancePrompt,
    negativePrompt: c.negativePrompt,
    triggerToken: c.triggerToken,
    profile: c.profile,
    pinned: c.pinned,
  };
}

function VoiceEditor({
  label,
  voice,
  presets,
  onSave,
  onPreview,
  previewing,
}: {
  label: string;
  voice?: CharacterVoice;
  presets: VoicePreset[];
  onSave: (engine: string, presetId: string | undefined, exaggeration: string) => void;
  onPreview?: (text: string) => void;
  previewing?: boolean;
}) {
  const [engine, setEngine] = useState(voice?.engine ?? TTS_ENGINES[0].id);
  const [presetId, setPresetId] = useState(voice?.voicePresetId ?? "");
  const [exaggeration, setExaggeration] = useState(voice?.params.exaggeration ?? "0.5");
  const [line, setLine] = useState("");
  useEffect(() => {
    setEngine(voice?.engine ?? TTS_ENGINES[0].id);
    setPresetId(voice?.voicePresetId ?? "");
    setExaggeration(voice?.params.exaggeration ?? "0.5");
  }, [voice?.engine, voice?.voicePresetId, voice?.params.exaggeration]);
  return (
    <div className="flex flex-col gap-2 rounded-md border border-border p-2">
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium">{label}</span>
        {voice ? <StatusChip state="done" label="Assigned" /> : <StatusChip state="none" label="No voice" />}
      </div>
      <div className="grid grid-cols-3 gap-2">
        <label className="flex flex-col gap-1 text-2xs text-text-2">
          Engine
          <select value={engine} onChange={(e) => setEngine(e.target.value)} className={inputClass}>
            {TTS_ENGINES.map((e) => (
              <option key={e.id} value={e.id}>
                {e.label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-2xs text-text-2">
          Preset
          <select value={presetId} onChange={(e) => setPresetId(e.target.value)} className={inputClass}>
            <option value="">Engine default voice</option>
            {presets.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-2xs text-text-2">
          Exaggeration <span className="font-mono">{exaggeration}</span>
          <input type="range" min="0" max="1" step="0.05" value={exaggeration} onChange={(e) => setExaggeration(e.target.value)} />
        </label>
      </div>
      <button type="button" className={`${buttonClass} self-start`} onClick={() => onSave(engine, presetId || undefined, exaggeration)}>
        Save voice
      </button>
      {onPreview && voice && (
        <div className="flex items-center gap-2">
          <input value={line} onChange={(e) => setLine(e.target.value)} placeholder="Preview line…" aria-label={`${label} preview line`} className={`${inputClass} flex-1`} maxLength={500} />
          <button type="button" className={buttonClass} disabled={!line.trim() || previewing} onClick={() => onPreview(line)}>
            {previewing ? "Synthesizing…" : "Preview line"}
          </button>
        </div>
      )}
      {voice?.previewAssetId && <audio controls preload="none" src={assetUrl(voice.previewAssetId)} aria-label={`${label} preview`} className="h-8 w-full" />}
    </div>
  );
}

function CharacterDetail({ seriesId, character, presets }: { seriesId: string; character: Character; presets: VoicePreset[] }) {
  const actions = useCharacterActions(seriesId);
  const upload = useMediaUpload("image");
  const [form, setForm] = useState<CharacterInput>(toInput(character));
  const [runId, setRunId] = useState<string | undefined>();
  const [error, setError] = useState<string | null>(null);
  const run = useRunProgress(seriesId, runId);
  useEffect(() => setForm(toInput(character)), [character]);
  const runActive = (run.data?.items ?? []).some((s) => !["done", "failed", "canceled"].includes(s.status));
  const runFailure = (run.data?.items ?? []).find((s) => s.status === "failed");
  const setName = (key: keyof CharacterInput["names"], value: string) => setForm((f) => ({ ...f, names: { ...f.names, [key]: value } }));
  const voice = (lang: VoiceLanguage) => character.voices.find((v) => v.lang === lang);
  const onError = (e: Error) => setError(e.message);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-semibold">{displayName(character)}</h2>
        <span className="text-xs text-text-2">
          {[character.names.orig, character.names.vi].filter(Boolean).join(" · ")}
          {character.appearances.length > 0 &&
            ` · Appears in ${character.appearances.map((a) => `Ep ${String(a.episodeIdx).padStart(2, "0")}`).join(", ")} · ${character.appearances.reduce((n, a) => n + a.sceneCount, 0)} scenes`}
        </span>
        <span className="flex-1" />
        <button type="button" className="text-xs text-destructive hover:underline" onClick={() => actions.remove.mutate(character.id)}>
          Delete
        </button>
      </div>

      <InspectorSection title="Reference sheet">
        <div className="flex flex-wrap gap-2">
          {character.refs.map((r) => (
            <figure key={r.id} className="flex w-36 flex-col gap-1">
              <img src={assetUrl(r.assetId, "webp-320")} onError={(e) => ((e.target as HTMLImageElement).src = assetUrl(r.assetId))} alt={`${displayName(character)} ${r.angle || "reference"}`} width={144} height={192} className="aspect-[3/4] w-full rounded-sm bg-well object-cover" />
              <figcaption className="flex flex-col gap-1 font-mono text-2xs text-muted-foreground">
                <input
                  aria-label="Angle"
                  defaultValue={r.angle}
                  placeholder="angle"
                  onBlur={(e) => e.target.value !== r.angle && actions.updateRef.mutate({ id: character.id, refId: r.id, angle: e.target.value })}
                  className="h-6 rounded-sm border border-input bg-well px-1"
                />
                <label className="flex items-center gap-1">
                  <input type="checkbox" checked={r.approved} onChange={(e) => actions.updateRef.mutate({ id: character.id, refId: r.id, approved: e.target.checked })} />
                  approved {r.origin === "generated" ? "· generated" : ""}
                </label>
                <button
                  type="button"
                  className={buttonClass}
                  disabled={runActive}
                  onClick={() => actions.sheet.mutate({ id: character.id, refId: r.id }, { onSuccess: (res) => setRunId(res?.runId), onError })}
                >
                  <RefreshCw size={12} aria-hidden="true" /> Regenerate sheet
                </button>
              </figcaption>
            </figure>
          ))}
          <label className="flex h-48 w-36 cursor-pointer flex-col items-center justify-center gap-1 rounded-sm border border-dashed border-border text-xs text-text-2 hover:bg-accent">
            <Upload size={16} aria-hidden="true" />
            {upload.busy ? "Uploading…" : "Upload refs"}
            <input
              type="file"
              accept="image/png,image/jpeg,image/webp"
              multiple
              className="sr-only"
              onChange={async (e) => {
                setError(null);
                for (const file of Array.from(e.target.files ?? [])) {
                  try {
                    const assetId = await upload.upload(file);
                    await actions.addRef.mutateAsync({ id: character.id, assetId });
                  } catch (err) {
                    setError((err as Error).message);
                  }
                }
              }}
            />
          </label>
        </div>
      </InspectorSection>

      <div className="grid grid-cols-2 gap-4">
        <InspectorSection title="Appearance">
          <div className="grid grid-cols-3 gap-2">
            {(["orig", "en", "vi"] as const).map((k) => (
              <label key={k} className="flex flex-col gap-1 text-2xs text-text-2">
                Name ({k === "orig" ? "original" : k.toUpperCase()})
                <input value={form.names[k]} onChange={(e) => setName(k, e.target.value)} className={inputClass} />
              </label>
            ))}
          </div>
          <label className="flex flex-col gap-1 text-2xs text-text-2">
            Role
            <input value={form.role ?? ""} onChange={(e) => setForm({ ...form, role: e.target.value })} className={inputClass} />
          </label>
          <label className="flex flex-col gap-1 text-2xs text-text-2">
            Trigger token
            <input value={form.triggerToken ?? ""} onChange={(e) => setForm({ ...form, triggerToken: e.target.value })} className={`${inputClass} font-mono`} />
          </label>
          <label className="flex flex-col gap-1 text-2xs text-text-2">
            Appearance prompt
            <textarea rows={3} value={form.appearancePrompt ?? ""} onChange={(e) => setForm({ ...form, appearancePrompt: e.target.value })} className="rounded-md border border-input bg-well p-2 text-sm" />
          </label>
          <label className="flex flex-col gap-1 text-2xs text-text-2">
            Negative prompt
            <input value={form.negativePrompt ?? ""} onChange={(e) => setForm({ ...form, negativePrompt: e.target.value })} className={inputClass} />
          </label>
        </InspectorSection>

        <InspectorSection title="Profile (sent to LLM)">
          <textarea rows={8} value={form.profile ?? ""} onChange={(e) => setForm({ ...form, profile: e.target.value })} aria-label="Profile" className="rounded-md border border-input bg-well p-2 font-serif text-sm" />
          <div className="flex items-center gap-3 text-xs text-text-2">
            <span className="font-mono tabular-nums">{estimateTokens(form.profile ?? "")} tok</span>
            <label className="flex items-center gap-1">
              <input type="checkbox" checked={form.pinned ?? true} onChange={(e) => setForm({ ...form, pinned: e.target.checked })} /> Pinned into every request of this series
            </label>
          </div>
          <button type="button" className={`${buttonClass} self-start`} disabled={actions.update.isPending} onClick={() => actions.update.mutate({ id: character.id, body: form }, { onError })}>
            <Check size={12} aria-hidden="true" /> Save character
          </button>
        </InspectorSection>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <InspectorSection title="LoRA">
          {character.loras.length === 0 ? (
            <p className="text-xs text-muted-foreground">No LoRA trained yet.</p>
          ) : (
            <ul className="flex flex-col gap-1 text-xs">
              {character.loras.map((l) => (
                <li key={l.id} className="flex items-center gap-2">
                  <span className="font-mono">v{l.version}</span>
                  <StatusChip state={l.status === "ready" ? "done" : l.status === "failed" ? "failed" : l.status === "training" ? "running" : "queued"} label={l.status} />
                  <span className="text-muted-foreground">{l.datasetSize} images</span>
                </li>
              ))}
            </ul>
          )}
          <button type="button" className={`${buttonClass} self-start`} disabled={runActive} onClick={() => actions.train.mutate(character.id, { onSuccess: (res) => setRunId(res?.runId), onError })}>
            Train LoRA v{character.loras.length + 1}
          </button>
          <span className="text-2xs text-muted-foreground">Trains on approved references; queues behind other GPU work at training priority.</span>
        </InspectorSection>

        <InspectorSection title="Voice">
          {LANGS.map((lang) => (
            <VoiceEditor
              key={lang}
              label={lang.toUpperCase()}
              voice={voice(lang)}
              presets={presets}
              previewing={runActive}
              onSave={(engine, voicePresetId, exaggeration) =>
                actions.setVoice.mutate({ id: character.id, lang, body: { engine, voicePresetId, params: { exaggeration } } }, { onError })
              }
              onPreview={(text) => actions.preview.mutate({ id: character.id, lang, text }, { onSuccess: (res) => setRunId(res?.runId), onError })}
            />
          ))}
        </InspectorSection>
      </div>

      {runFailure && (
        <p role="alert" className="text-xs text-destructive">
          {runFailure.kind} failed: {runFailure.errorMsg || runFailure.errorCode}
        </p>
      )}
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

/** Characters of a series (wireframe characters): list on the left, detail on the right, plus the narrator's voices. */
export function CharactersView({ seriesId }: { seriesId: string }) {
  const series = useQuery(getSeriesOptions({ path: { id: seriesId } }));
  const characters = useCharacters(seriesId);
  const presets = useQuery(listVoicePresetsOptions());
  const actions = useCharacterActions(seriesId);
  const [selectedId, setSelectedId] = useState<string | undefined>();
  const [filter, setFilter] = useState("");
  const [newName, setNewName] = useState("");
  const items = characters.data?.items ?? [];
  const visible = items.filter((c) => `${c.names.orig} ${c.names.en} ${c.names.vi}`.toLowerCase().includes(filter.toLowerCase()));
  const selected = items.find((c) => c.id === selectedId) ?? items[0];
  const narrator = (lang: VoiceLanguage) => characters.data?.narratorVoices.find((v) => v.lang === lang);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <div className="flex flex-col">
          <Link to="/projects/$seriesId" params={{ seriesId }} className="text-xs text-text-2 hover:underline">
            {series.data?.title ?? "Series"}
          </Link>
          <h1 className="text-lg font-semibold">Characters</h1>
        </div>
        <span className="flex-1" />
        <span className="text-xs text-text-2">
          Pinned profiles: <span className="font-mono">{characters.data?.pinnedTokens ?? 0} tok</span> per request
        </span>
      </div>
      <div className="grid grid-cols-[260px_minmax(0,1fr)] gap-4">
        <aside className="flex flex-col gap-2 border-r border-border pr-3">
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter characters" aria-label="Filter characters" className={inputClass} />
          <form
            className="flex gap-1"
            onSubmit={(e) => {
              e.preventDefault();
              if (!newName.trim()) return;
              const body = emptyInput();
              body.names.en = newName.trim();
              actions.create.mutate(body, { onSuccess: (c) => c && setSelectedId(c.id) });
              setNewName("");
            }}
          >
            <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="New character name" aria-label="New character name" className={`${inputClass} flex-1`} />
            <button type="submit" aria-label="New character" className={buttonClass}>
              <Plus size={14} aria-hidden="true" />
            </button>
          </form>
          <ul role="listbox" aria-label="Characters" className="flex flex-col">
            {visible.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  role="option"
                  aria-selected={selected?.id === c.id}
                  onClick={() => setSelectedId(c.id)}
                  className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm ${selected?.id === c.id ? "bg-card" : "hover:bg-accent"}`}
                >
                  <span className="flex-1 truncate">{displayName(c)}</span>
                  {c.loras.some((l) => l.status === "ready") ? (
                    <span className="font-mono text-2xs text-primary-text">LoRA v{Math.max(...c.loras.filter((l) => l.status === "ready").map((l) => l.version))}</span>
                  ) : (
                    <span className="text-2xs text-muted-foreground">{c.refs.length ? "Refs only" : "No refs"}</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
          <InspectorSection title="Narrator voice">
            {LANGS.map((lang) => (
              <VoiceEditor
                key={lang}
                label={`Narrator ${lang.toUpperCase()}`}
                voice={narrator(lang)}
                presets={presets.data?.items ?? []}
                onSave={(engine, voicePresetId, exaggeration) => actions.setNarrator.mutate({ lang, body: { engine, voicePresetId, params: { exaggeration } } })}
              />
            ))}
          </InspectorSection>
        </aside>
        <section aria-label="Character detail">
          {selected ? (
            <CharacterDetail key={selected.id} seriesId={seriesId} character={selected} presets={presets.data?.items ?? []} />
          ) : (
            <EmptyState message="No characters yet. Add the cast so the scene split can attribute dialogue and voice it." />
          )}
        </section>
      </div>
    </div>
  );
}
