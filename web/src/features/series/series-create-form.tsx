import { useState } from "react";

import type { SeriesCreateRequest, TargetLanguage } from "../../api/gen/types.gen";
import { ApiError } from "../../api/client";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { useCreateSeries } from "./use-series";

const LANGUAGES: { value: TargetLanguage; label: string }[] = [
  { value: "en", label: "English" },
  { value: "vi", label: "Vietnamese" },
];

/** Series settings form (phase 6): title, genre, target language(s), target episode minutes, planned episode count, style notes. */
export function SeriesCreateForm({ onCreated }: { onCreated: (seriesId: string) => void }) {
  const createSeries = useCreateSeries();
  const [title, setTitle] = useState("");
  const [genre, setGenre] = useState("");
  const [languages, setLanguages] = useState<TargetLanguage[]>(["en"]);
  const [minutes, setMinutes] = useState(30);
  const [episodeCount, setEpisodeCount] = useState(12);
  const [styleNotes, setStyleNotes] = useState("");

  const toggleLanguage = (lang: TargetLanguage) => {
    setLanguages((current) => (current.includes(lang) ? current.filter((l) => l !== lang) : [...current, lang]));
  };

  const handleSubmit = (event: React.FormEvent) => {
    event.preventDefault();
    if (!title.trim() || languages.length === 0) return;
    const body: SeriesCreateRequest = {
      title: title.trim(),
      genre: genre.trim() || undefined,
      targetLanguages: languages,
      targetEpisodeMinutes: minutes,
      plannedEpisodeCount: episodeCount,
      styleNotes: styleNotes.trim() || undefined,
    };
    createSeries.mutate(
      { body },
      { onSuccess: (series) => series && onCreated(series.id) },
    );
  };

  return (
    <form onSubmit={handleSubmit} className="flex max-w-md flex-col gap-4 rounded-md border border-border bg-card p-4">
      <h2 className="text-md font-medium">New series</h2>

      <label className="flex flex-col gap-1.5 text-sm">
        Title
        <Input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} required />
      </label>

      <label className="flex flex-col gap-1.5 text-sm">
        Genre
        <Input value={genre} onChange={(e) => setGenre(e.target.value)} maxLength={100} placeholder="Xianxia" />
      </label>

      <fieldset className="flex flex-col gap-1.5 text-sm">
        <legend className="mb-1">Target language(s)</legend>
        <div className="flex gap-3">
          {LANGUAGES.map((lang) => (
            <label key={lang.value} className="flex items-center gap-1.5 text-sm text-text-2">
              <input
                type="checkbox"
                checked={languages.includes(lang.value)}
                onChange={() => toggleLanguage(lang.value)}
              />
              {lang.label}
            </label>
          ))}
        </div>
      </fieldset>

      <label className="flex flex-col gap-1.5 text-sm">
        Target episode minutes
        <Input
          type="number"
          min={1}
          max={120}
          value={minutes}
          onChange={(e) => setMinutes(Number(e.target.value))}
        />
      </label>

      <label className="flex flex-col gap-1.5 text-sm">
        Planned episode count
        <Input
          type="number"
          min={1}
          max={500}
          value={episodeCount}
          onChange={(e) => setEpisodeCount(Number(e.target.value))}
        />
      </label>

      <label className="flex flex-col gap-1.5 text-sm">
        Style notes
        <textarea
          className="min-h-20 w-full rounded-md border border-input bg-well px-2.5 py-2 text-sm text-foreground"
          value={styleNotes}
          onChange={(e) => setStyleNotes(e.target.value)}
          maxLength={4000}
        />
      </label>

      {createSeries.isError && (
        <p className="text-xs text-destructive">{(createSeries.error as ApiError).message}</p>
      )}

      <Button type="submit" variant="primary" size="md" disabled={createSeries.isPending}>
        {createSeries.isPending ? "Creating…" : "Create series"}
      </Button>
    </form>
  );
}
