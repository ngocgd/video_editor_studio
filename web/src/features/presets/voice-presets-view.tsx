import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import {
  createVoicePresetMutation,
  deleteVoicePresetMutation,
  listVoicePresetsOptions,
  listVoicePresetsQueryKey,
  updateVoicePresetMutation,
} from "../../api/gen/@tanstack/react-query.gen";
import type { Problem, VoicePreset, VoicePresetInput } from "../../api/gen/types.gen";
import { EmptyState } from "../../components/shared/empty-state";
import { useMediaUpload } from "./use-media-upload";

/** TTS engines the Python worker offers (manifest entries with task tts). */
export const TTS_ENGINES = [
  { id: "chatterbox", label: "Chatterbox (EN)" },
  { id: "vieneu-v3-turbo", label: "VieNeu-TTS (VI)" },
];

const inputClass = "h-8 rounded-md border border-input bg-well px-2 text-sm";

function PresetForm({ preset, onDone }: { preset?: VoicePreset; onDone: () => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(preset?.name ?? "");
  const [engine, setEngine] = useState(preset?.engine ?? TTS_ENGINES[0].id);
  const [exaggeration, setExaggeration] = useState(preset?.params.exaggeration ?? "0.5");
  const [refAssetId, setRefAssetId] = useState<string | undefined>(preset?.refAudioAssetId);
  const [consent, setConsent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const upload = useMediaUpload("audio");
  const handlers = {
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listVoicePresetsQueryKey() });
      onDone();
    },
    onError: (e: Problem) => setError(e.detail ?? e.title),
  };
  const create = useMutation({ ...createVoicePresetMutation(), ...handlers });
  const update = useMutation({ ...updateVoicePresetMutation(), ...handlers });
  const pending = create.isPending || update.isPending;

  const needsConsent = Boolean(refAssetId) && !(preset?.consented && preset.refAudioAssetId === refAssetId);
  const submit = () => {
    const body: VoicePresetInput = { name, engine, params: { exaggeration }, refAudioAssetId: refAssetId, consent: consent || undefined };
    if (preset) update.mutate({ path: { id: preset.id }, body });
    else create.mutate({ body });
  };

  return (
    <form
      className="flex max-w-xl flex-col gap-3 rounded-md border border-border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Name
        <input required value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Engine
        <select value={engine} onChange={(e) => setEngine(e.target.value)} className={inputClass}>
          {TTS_ENGINES.map((e) => (
            <option key={e.id} value={e.id}>
              {e.label}
            </option>
          ))}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Exaggeration <span className="font-mono">{exaggeration}</span>
        <input type="range" min="0" max="1" step="0.05" value={exaggeration} onChange={(e) => setExaggeration(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Reference voice (optional, WAV/FLAC/MP3)
        <input
          type="file"
          accept="audio/wav,audio/x-wav,audio/flac,audio/mpeg"
          onChange={async (e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            setError(null);
            try {
              setRefAssetId(await upload.upload(file));
              setConsent(false);
            } catch (err) {
              setError((err as Error).message);
            }
          }}
        />
        {refAssetId && <span className="text-2xs text-muted-foreground">Reference uploaded.</span>}
      </label>
      {needsConsent && (
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
          <span>This is my own voice, or I hold a licence to clone it. This confirmation is recorded in the audit log.</span>
        </label>
      )}
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <button type="submit" disabled={pending || upload.busy || (needsConsent && !consent)} className="h-8 rounded-md bg-primary px-3 text-sm text-primary-foreground disabled:opacity-50">
          {preset ? "Save preset" : "Create preset"}
        </button>
        <button type="button" onClick={onDone} className="h-8 rounded-md border border-border px-3 text-sm">
          Cancel
        </button>
      </div>
    </form>
  );
}

/** Settings > Voices: TTS voice presets, optionally cloned from a consented reference. */
export function VoicePresetsView() {
  const queryClient = useQueryClient();
  const presets = useQuery(listVoicePresetsOptions());
  const [editing, setEditing] = useState<VoicePreset | "new" | null>(null);
  const remove = useMutation({
    ...deleteVoicePresetMutation(),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: listVoicePresetsQueryKey() }),
  });
  const items = presets.data?.items ?? [];
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Voice presets</h1>
        <button type="button" onClick={() => setEditing("new")} className="h-8 rounded-md bg-primary px-3 text-sm text-primary-foreground">
          New preset
        </button>
      </div>
      {editing && <PresetForm key={editing === "new" ? "new" : editing.id} preset={editing === "new" ? undefined : editing} onDone={() => setEditing(null)} />}
      {items.length === 0 ? (
        <EmptyState message="No voice presets yet. Create one, then assign it to characters and the narrator." />
      ) : (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
              <th className="h-8 px-3">Name</th>
              <th className="h-8 px-3">Engine</th>
              <th className="h-8 px-3">Reference</th>
              <th className="h-8 px-3" />
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <tr key={p.id} className="border-b border-border">
                <td className="h-9 px-3">{p.name}</td>
                <td className="h-9 px-3 font-mono text-xs text-text-2">{p.engine}</td>
                <td className="h-9 px-3 text-text-2">{p.refAudioAssetId ? (p.consented ? "Cloned · consent recorded" : "Cloned") : "Built-in voice"}</td>
                <td className="h-9 px-3 text-right">
                  <button type="button" onClick={() => setEditing(p)} className="mr-2 text-xs text-primary-text hover:underline">
                    Edit
                  </button>
                  <button type="button" onClick={() => remove.mutate({ path: { id: p.id } })} className="text-xs text-destructive hover:underline">
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {remove.error && (
        <p role="alert" className="text-xs text-destructive">
          {remove.error.detail ?? remove.error.title}
        </p>
      )}
    </div>
  );
}
