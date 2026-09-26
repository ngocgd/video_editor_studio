import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import {
  createImageStyleMutation,
  deleteImageStyleMutation,
  listImageStylesOptions,
  listImageStylesQueryKey,
  listModelsOptions,
  updateImageStyleMutation,
} from "../../api/gen/@tanstack/react-query.gen";
import type { Problem, ImageStyle, ImageStyleInput } from "../../api/gen/types.gen";
import { EmptyState } from "../../components/shared/empty-state";

const inputClass = "h-8 rounded-md border border-input bg-well px-2 text-sm";

function StyleForm({ style, onDone }: { style?: ImageStyle; onDone: () => void }) {
  const queryClient = useQueryClient();
  const models = useQuery(listModelsOptions());
  const sceneModels = (models.data?.items ?? []).filter((m) => m.task === "scene");
  const [form, setForm] = useState<ImageStyleInput>({
    name: style?.name ?? "",
    baseModel: style?.baseModel ?? "z-image-turbo",
    stylePrompt: style?.stylePrompt ?? "",
    negativePrompt: style?.negativePrompt ?? "",
    sampler: style?.sampler ?? "",
    steps: style?.steps ?? 8,
    width: style?.width ?? 1920,
    height: style?.height ?? 1080,
    loras: style?.loras ?? [],
  });
  const [error, setError] = useState<string | null>(null);
  const handlers = {
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: listImageStylesQueryKey() });
      onDone();
    },
    onError: (e: Problem) => setError(e.detail ?? e.title),
  };
  const create = useMutation({ ...createImageStyleMutation(), ...handlers });
  const update = useMutation({ ...updateImageStyleMutation(), ...handlers });
  const set = <K extends keyof ImageStyleInput>(key: K, value: ImageStyleInput[K]) => setForm((f) => ({ ...f, [key]: value }));
  const lora = form.loras?.[0];

  return (
    <form
      className="grid max-w-2xl grid-cols-2 gap-3 rounded-md border border-border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (style) update.mutate({ path: { id: style.id }, body: form });
        else create.mutate({ body: form });
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Name
        <input required value={form.name} onChange={(e) => set("name", e.target.value)} className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Base model
        <select value={form.baseModel} onChange={(e) => set("baseModel", e.target.value)} className={inputClass}>
          {sceneModels.length === 0 && <option value={form.baseModel}>{form.baseModel}</option>}
          {sceneModels.map((m) => (
            <option key={m.name} value={m.name}>
              {m.title || m.name}
            </option>
          ))}
        </select>
      </label>
      <label className="col-span-2 flex flex-col gap-1 text-xs text-text-2">
        Style prompt
        <textarea rows={2} value={form.stylePrompt} onChange={(e) => set("stylePrompt", e.target.value)} className="rounded-md border border-input bg-well p-2 text-sm" />
      </label>
      <label className="col-span-2 flex flex-col gap-1 text-xs text-text-2">
        Negative prompt
        <textarea rows={2} value={form.negativePrompt} onChange={(e) => set("negativePrompt", e.target.value)} className="rounded-md border border-input bg-well p-2 text-sm" />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Steps
        <input type="number" min={1} max={150} value={form.steps} onChange={(e) => set("steps", Number(e.target.value))} className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Sampler
        <input value={form.sampler} onChange={(e) => set("sampler", e.target.value)} placeholder="workflow default" className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Width
        <input type="number" min={64} max={4096} value={form.width} onChange={(e) => set("width", Number(e.target.value))} className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Height
        <input type="number" min={64} max={4096} value={form.height} onChange={(e) => set("height", Number(e.target.value))} className={inputClass} />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        Style LoRA file (optional)
        <input
          value={lora?.name ?? ""}
          onChange={(e) => set("loras", e.target.value ? [{ name: e.target.value, strength: lora?.strength ?? 0.8 }] : [])}
          className={inputClass}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-text-2">
        LoRA strength
        <input
          type="number"
          step={0.05}
          min={-2}
          max={2}
          disabled={!lora}
          value={lora?.strength ?? 0.8}
          onChange={(e) => lora && set("loras", [{ name: lora.name, strength: Number(e.target.value) }])}
          className={inputClass}
        />
      </label>
      {error && (
        <p role="alert" className="col-span-2 text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="col-span-2 flex gap-2">
        <button type="submit" disabled={create.isPending || update.isPending} className="h-8 rounded-md bg-primary px-3 text-sm text-primary-foreground disabled:opacity-50">
          {style ? "Save style" : "Create style"}
        </button>
        <button type="button" onClick={onDone} className="h-8 rounded-md border border-border px-3 text-sm">
          Cancel
        </button>
      </div>
    </form>
  );
}

/** Settings > Styles: image styles scenes are generated with. */
export function ImageStylesView() {
  const queryClient = useQueryClient();
  const styles = useQuery(listImageStylesOptions());
  const [editing, setEditing] = useState<ImageStyle | "new" | null>(null);
  const remove = useMutation({
    ...deleteImageStyleMutation(),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: listImageStylesQueryKey() }),
  });
  const items = styles.data?.items ?? [];
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Image styles</h1>
        <button type="button" onClick={() => setEditing("new")} className="h-8 rounded-md bg-primary px-3 text-sm text-primary-foreground">
          New style
        </button>
      </div>
      {editing && <StyleForm key={editing === "new" ? "new" : editing.id} style={editing === "new" ? undefined : editing} onDone={() => setEditing(null)} />}
      {items.length === 0 ? (
        <EmptyState message="No image styles yet. Scenes need one to generate images." />
      ) : (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-border text-left text-2xs uppercase tracking-[0.04em] text-muted-foreground">
              <th className="h-8 px-3">Name</th>
              <th className="h-8 px-3">Model</th>
              <th className="h-8 px-3">Size</th>
              <th className="h-8 px-3">Steps</th>
              <th className="h-8 px-3" />
            </tr>
          </thead>
          <tbody>
            {items.map((s) => (
              <tr key={s.id} className="border-b border-border">
                <td className="h-9 px-3">{s.name}</td>
                <td className="h-9 px-3 font-mono text-xs text-text-2">{s.baseModel}</td>
                <td className="h-9 px-3 font-mono text-xs text-text-2">
                  {s.width}×{s.height}
                </td>
                <td className="h-9 px-3 font-mono text-xs text-text-2">{s.steps}</td>
                <td className="h-9 px-3 text-right">
                  <button type="button" onClick={() => setEditing(s)} className="mr-2 text-xs text-primary-text hover:underline">
                    Edit
                  </button>
                  <button type="button" onClick={() => remove.mutate({ path: { id: s.id } })} className="text-xs text-destructive hover:underline">
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
