import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import {
  getStoryboardSettingsOptions,
  getStoryboardSettingsQueryKey,
  putStoryboardSettingsMutation,
} from "../../api/gen/@tanstack/react-query.gen";
import type { ImageStyle, StoryboardSettings } from "../../api/gen/types.gen";

const inputClass = "h-7 w-16 rounded-md border border-input bg-well px-1 font-mono text-xs";

/** Series storyboard defaults: image-change cadence, segment gap and default image style. */
export function StoryboardSettingsPanel({ seriesId, styles }: { seriesId: string; styles: ImageStyle[] }) {
  const queryClient = useQueryClient();
  const settings = useQuery(getStoryboardSettingsOptions({ path: { id: seriesId } }));
  const [form, setForm] = useState<StoryboardSettings>({ cadenceMinS: 20, cadenceMaxS: 40, segmentGapMs: 150 });
  useEffect(() => {
    if (settings.data) setForm(settings.data);
  }, [settings.data]);
  const save = useMutation({
    ...putStoryboardSettingsMutation(),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: getStoryboardSettingsQueryKey({ path: { id: seriesId } }) }),
  });
  const num = (key: "cadenceMinS" | "cadenceMaxS" | "segmentGapMs") => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: Number(e.target.value) }));

  return (
    <details className="relative text-xs">
      <summary className="flex h-8 cursor-pointer list-none items-center rounded-md border border-border bg-secondary px-3 text-sm hover:bg-accent">Scene settings</summary>
      <form
        className="absolute right-0 z-10 mt-1 flex w-72 flex-col gap-2 rounded-md border border-border bg-popover p-3 shadow-lg"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate({ path: { id: seriesId }, body: form });
        }}
      >
        <label className="flex items-center justify-between gap-2 text-text-2">
          Image change every
          <span className="flex items-center gap-1">
            <input type="number" min={5} max={300} value={form.cadenceMinS} onChange={num("cadenceMinS")} aria-label="Cadence minimum seconds" className={inputClass} />–
            <input type="number" min={5} max={600} value={form.cadenceMaxS} onChange={num("cadenceMaxS")} aria-label="Cadence maximum seconds" className={inputClass} />s
          </span>
        </label>
        <label className="flex items-center justify-between gap-2 text-text-2">
          Gap between speakers
          <span className="flex items-center gap-1">
            <input type="number" min={0} max={5000} step={10} value={form.segmentGapMs} onChange={num("segmentGapMs")} aria-label="Segment gap milliseconds" className={inputClass} />
            ms
          </span>
        </label>
        <label className="flex flex-col gap-1 text-text-2">
          Default image style
          <select
            value={form.imageStyleId ?? ""}
            onChange={(e) => setForm((f) => ({ ...f, imageStyleId: e.target.value || undefined }))}
            className="h-7 rounded-md border border-input bg-well px-1 text-xs text-foreground"
          >
            <option value="">Oldest style of the workspace</option>
            {styles.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        <p className="text-2xs text-muted-foreground">The cadence applies to the next split; the gap to the next voice take.</p>
        {save.error && <p role="alert" className="text-destructive">{save.error.detail ?? save.error.title}</p>}
        <button type="submit" disabled={save.isPending} className="h-7 self-start rounded-md bg-primary px-3 text-primary-foreground disabled:opacity-50">
          {save.isSuccess ? "Saved" : "Save"}
        </button>
      </form>
    </details>
  );
}
