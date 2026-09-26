import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Scissors, Search, Sparkles } from "lucide-react";
import { type MouseEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  getEpisodeOptions,
  getRunOptions,
  getSeriesOptions,
  listCharactersOptions,
  listImageStylesOptions,
} from "../../api/gen/@tanstack/react-query.gen";
import type { Scene, SceneFilter, SceneLanguage, SceneSplitRequest } from "../../api/gen/types.gen";
import { ApiError } from "../../api/client";
import { useSseTopics } from "../../api/use-sse-topics";
import { EmptyState } from "../../components/shared/empty-state";
import type { Speaker } from "../../components/shared/speaker-monogram";
import { popShortcutScope, pushShortcutScope, useShortcut } from "../../lib/shortcuts";
import { Timeline, type TimelineClip } from "../timeline/timeline";
import { SceneGrid } from "./scene-grid";
import { SceneInspector } from "./scene-inspector";
import { SceneFilterChips } from "./scene-filter-chips";
import { ResplitConfirmDialog } from "./resplit-confirm-dialog";
import { StoryboardSettingsPanel } from "./storyboard-settings";
import { nextMotion, PIP_LABELS, rangeSelection } from "./storyboard-model";
import { useRenders } from "../render/use-render";
import { assetUrl, useGenerateMissing, useRegenerate, useScenes, useSplitScenes, useUpdateScene } from "./use-scenes";

const SCOPE = "storyboard";

function clipState(scene: Scene): TimelineClip["state"] {
  const image = scene.pips.find((p) => p.kind === "image")?.state;
  if (scene.worstState === "failed") return "failed";
  if (scene.worstState === "stale") return "stale";
  return image === "done" ? "done" : "missing";
}

/**
 * Storyboard and scene editor (wireframe storyboard-scene-editor): filter
 * chips with server counts, stage progress, the virtualized scene grid,
 * the canvas timeline and the scene inspector, with the storyboard keys
 * (J/K, Space, E, M, I, V, arrows, Shift+Click range select).
 */
export function StoryboardView({ seriesId, episodeId }: { seriesId: string; episodeId: string }) {
  const queryClient = useQueryClient();
  const series = useQuery(getSeriesOptions({ path: { id: seriesId } }));
  const episode = useQuery(getEpisodeOptions({ path: { id: episodeId } }));
  const characters = useQuery(listCharactersOptions({ path: { id: seriesId } }));
  const styles = useQuery(listImageStylesOptions());
  const [lang, setLang] = useState<SceneLanguage>("en");
  const [filter, setFilter] = useState<SceneFilter>("all");
  const [search, setSearch] = useState("");
  const [q, setQ] = useState("");
  const scenesQuery = useScenes(episodeId, lang, filter, q);
  const allQuery = useScenes(episodeId, lang, "all", "");
  const scenes = useMemo(() => scenesQuery.data?.items ?? [], [scenesQuery.data]);
  const allScenes = useMemo(() => allQuery.data?.items ?? [], [allQuery.data]);
  const [activeId, setActiveId] = useState<string | undefined>();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState(0);
  const [editingNarration, setEditingNarration] = useState(false);
  const [splitRunId, setSplitRunId] = useState<string | undefined>();
  const [confirmSplit, setConfirmSplit] = useState<{ body: SceneSplitRequest; message: string } | undefined>();
  const [playing, setPlaying] = useState(false);
  const [playheadMs, setPlayheadMs] = useState<number | undefined>();
  const audioRef = useRef<HTMLAudioElement | null>(null);

  const split = useSplitScenes(episodeId);
  const generate = useGenerateMissing(episodeId);
  const regenerate = useRegenerate(episodeId);
  const update = useUpdateScene(episodeId);
  const renders = useRenders(episodeId, lang);
  // A re-split that would delete edited scenes or takes answers 409 with
  // the counts; ask before re-sending it with discardWork.
  const runSplit = (body: SceneSplitRequest) =>
    split.mutate(body, {
      onSuccess: (r) => {
        setConfirmSplit(undefined);
        if (r?.runId) setSplitRunId(r.runId);
      },
      onError: (e) => {
        if (e instanceof ApiError && e.status === 409) setConfirmSplit({ body, message: e.detail ?? e.message });
      },
    });
  const splitConflict = split.error instanceof ApiError && split.error.status === 409;

  useEffect(() => {
    const t = setTimeout(() => setQ(search), 200);
    return () => clearTimeout(t);
  }, [search]);

  useEffect(() => {
    const first = series.data?.targetLanguages[0];
    if (first) setLang(first);
  }, [series.data?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // An LLM split runs as a step: watch its run and refetch when it lands.
  useSseTopics(splitRunId ? [splitRunId] : []);
  const splitRun = useQuery({
    ...getRunOptions({ path: { id: splitRunId ?? "" } }),
    enabled: Boolean(splitRunId),
    refetchInterval: (s) => (s.state.data?.status === "active" ? 2000 : false),
  });
  useEffect(() => {
    if (splitRun.data && splitRun.data.status !== "active") {
      setSplitRunId(undefined);
      void queryClient.invalidateQueries({ predicate: (qq) => (qq.queryKey[0] as { _id?: string })?._id === "listScenes" });
    }
  }, [splitRun.data?.status]); // eslint-disable-line react-hooks/exhaustive-deps

  const activeIndex = Math.max(0, scenes.findIndex((s) => s.id === activeId));
  const active = scenes[activeIndex] ?? allScenes.find((s) => s.id === activeId);
  useEffect(() => {
    if (!activeId && scenes.length > 0) setActiveId(scenes[0].id);
  }, [scenes.length, activeId]); // eslint-disable-line react-hooks/exhaustive-deps

  const speakersById = useMemo(() => {
    const map = new Map<string, Speaker>();
    map.set("narrator", { id: "narrator", name: "Narrator", colorIndex: 0 });
    (characters.data?.items ?? []).forEach((c, i) => {
      map.set(c.id, { id: c.id, name: (lang === "vi" ? c.names.vi : c.names.en) || c.names.orig || c.names.en || c.names.vi, colorIndex: (i % 5) + 1 });
    });
    return map;
  }, [characters.data, lang]);

  const clips: TimelineClip[] = useMemo(
    () =>
      allScenes.map((s) => ({
        id: s.id,
        idx: s.idx,
        startMs: s.startMs,
        durationMs: Math.max(1, s.durationMs),
        label: s.segments[0]?.text ?? s.narration,
        state: clipState(s),
        hasPeaks: s.peaks,
      })),
    [allScenes],
  );

  // Stable callbacks (reading the latest state through a ref) keep the
  // memoized tiles and timeline from re-rendering on every move.
  const latest = useRef({ scenes, anchor });
  latest.current = { scenes, anchor };
  const activate = useCallback((index: number) => {
    const s = latest.current.scenes[index];
    if (!s) return;
    setActiveId(s.id);
    setAnchor(index);
    setSelected(new Set([s.id]));
    setEditingNarration(false);
  }, []);
  const onTileClick = useCallback(
    (index: number, event: MouseEvent<HTMLButtonElement>) => {
      const { scenes: list, anchor: from } = latest.current;
      if (event.shiftKey) {
        setSelected(rangeSelection(list, from, index));
        setActiveId(list[index]?.id);
        return;
      }
      activate(index);
    },
    [activate],
  );
  const onTimelineSelect = useCallback(
    (id: string) => {
      const i = latest.current.scenes.findIndex((s) => s.id === id);
      if (i >= 0) activate(i);
      else setActiveId(id);
    },
    [activate],
  );

  const stopAudio = () => {
    audioRef.current?.pause();
    setPlaying(false);
  };
  const togglePlay = () => {
    if (playing) {
      stopAudio();
      return;
    }
    if (!active?.voiceAssetId) return;
    const audio = audioRef.current ?? new Audio();
    audioRef.current = audio;
    audio.src = assetUrl(active.voiceAssetId);
    const start = active.startMs;
    audio.ontimeupdate = () => setPlayheadMs(start + audio.currentTime * 1000);
    audio.onended = () => setPlaying(false);
    void audio.play().then(() => setPlaying(true)).catch(() => setPlaying(false));
  };
  useEffect(() => () => audioRef.current?.pause(), []);

  useEffect(() => {
    pushShortcutScope(SCOPE);
    return () => popShortcutScope(SCOPE);
  }, []);
  const move = (delta: number) => {
    stopAudio();
    activate(Math.max(0, Math.min(scenes.length - 1, activeIndex + delta)));
  };
  useShortcut(SCOPE, "j", () => move(-1));
  useShortcut(SCOPE, "k", () => move(1));
  useShortcut(SCOPE, " ", () => togglePlay());
  useShortcut(SCOPE, "e", () => active && setEditingNarration(true));
  useShortcut(SCOPE, "escape", () => setEditingNarration(false));
  useShortcut(SCOPE, "i", () => active && regenerate.mutate({ id: active.id, kind: "image" }));
  useShortcut(SCOPE, "v", () => active && regenerate.mutate({ id: active.id, kind: "voice" }));
  useShortcut(SCOPE, "m", () => active && update.mutate({ id: active.id, body: { expectedVersion: active.version, motionPreset: nextMotion(active.motionPreset) } }));

  const data = scenesQuery.data;
  const langs = series.data?.targetLanguages ?? ["en"];
  const buttonClass = "flex h-8 items-center gap-1.5 rounded-md border border-border bg-secondary px-3 text-sm hover:bg-accent disabled:opacity-50";

  return (
    <div className="-m-4 grid h-[calc(100%+2rem)] min-h-0 grid-cols-[minmax(0,1fr)_360px]">
      <section aria-label="Storyboard" className="flex min-h-0 flex-col gap-3 p-4">
        <header className="flex flex-wrap items-center gap-2">
          <div className="flex flex-col">
            <span className="text-xs text-text-2">
              <Link to="/projects/$seriesId" params={{ seriesId }} className="hover:underline">
                {series.data?.title ?? "Series"}
              </Link>{" "}
              · Ep {String(episode.data?.idx ?? 0).padStart(2, "0")}
            </span>
            <h1 className="text-lg font-semibold">{episode.data?.title ?? "Storyboard"}</h1>
          </div>
          <nav aria-label="Episode" className="ml-4 flex gap-1 text-sm">
            <Link to="/projects/$seriesId/episodes/$episodeId" params={{ seriesId, episodeId }} className="rounded-md px-2 py-1 text-text-2 hover:bg-accent">
              Draft
            </Link>
            <span aria-current="page" className="rounded-md bg-accent px-2 py-1">
              Storyboard
            </span>
            <Link to="/projects/$seriesId/render/$episodeId" params={{ seriesId, episodeId }} className="rounded-md px-2 py-1 text-text-2 hover:bg-accent">
              Render
            </Link>
          </nav>
          <span className="flex-1" />
          <div role="radiogroup" aria-label="Language" className="flex gap-1">
            {langs.map((l) => (
              <button
                key={l}
                type="button"
                role="radio"
                aria-checked={lang === l}
                onClick={() => setLang(l)}
                className={`h-7 rounded-md border px-2 font-mono text-xs ${lang === l ? "border-primary bg-primary-muted" : "border-border"}`}
              >
                {l.toUpperCase()}
              </button>
            ))}
          </div>
          <StoryboardSettingsPanel seriesId={seriesId} styles={styles.data?.items ?? []} />
          <button type="button" className={buttonClass} disabled={split.isPending} onClick={() => runSplit({ lang, mode: "paragraphs" })}>
            <Scissors size={14} aria-hidden="true" /> Split by paragraphs
          </button>
          <button
            type="button"
            className={buttonClass}
            disabled={split.isPending || Boolean(splitRunId)}
            onClick={() => runSplit({ lang, mode: "llm" })}
          >
            <Sparkles size={14} aria-hidden="true" /> {splitRunId ? "Splitting with AI…" : "Re-split with AI"}
          </button>
          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground disabled:opacity-50"
            disabled={generate.isPending || !data || data.missingCount === 0}
            onClick={() => generate.mutate(lang)}
          >
            Generate missing <span className="font-mono">({data?.missingCount ?? 0})</span>
          </button>
        </header>
        {((split.error && !splitConflict) || generate.error) && (
          <p role="alert" className="text-sm text-destructive">
            {(splitConflict ? generate.error : (split.error ?? generate.error))?.message}
          </p>
        )}
        {split.data && split.data.mode === "paragraphs" && (
          <p role="status" className="text-xs text-text-2">
            Split into {split.data.sceneCount} scenes; kept {split.data.keptCount} unchanged with their takes; deleted {split.data.droppedCount ?? 0}.
          </p>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <SceneFilterChips value={filter} counts={data?.counts} onChange={setFilter} />
          <span className="flex-1" />
          <label className="flex h-7 items-center gap-1 rounded-md border border-input bg-well px-2 text-xs">
            <Search size={14} aria-hidden="true" />
            <span className="sr-only">Find in narration</span>
            <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Find in narration…" className="w-44 bg-transparent outline-none" />
          </label>
        </div>

        <ul aria-label="Pipeline progress" className="grid grid-cols-5 gap-2">
          {(data?.stages ?? []).map((st) => (
            <li key={st.kind} className="flex flex-col gap-1 text-xs">
              <span className="flex justify-between text-text-2">
                {PIP_LABELS[st.kind]}
                <span className="font-mono tabular-nums">
                  {st.done}/{st.total}
                </span>
              </span>
              <span className="h-1 overflow-hidden rounded-full bg-muted">
                <span className="block h-full bg-primary" style={{ width: `${st.total ? (st.done / st.total) * 100 : 0}%` }} />
              </span>
            </li>
          ))}
        </ul>

        {scenesQuery.isLoading ? (
          <p className="text-sm text-text-2">Loading scenes…</p>
        ) : allScenes.length === 0 ? (
          <EmptyState message={splitRunId ? "Splitting the draft into scenes with AI…" : "No scenes yet. Split the draft by paragraphs, or with AI to attribute dialogue to characters."} />
        ) : scenes.length === 0 ? (
          <p className="text-sm text-text-2">No scenes match this filter.</p>
        ) : (
          <SceneGrid scenes={scenes} activeIndex={activeIndex} selectedIds={selected} speakersById={speakersById} onActivate={activate} onTileClick={onTileClick} />
        )}
        {selected.size > 1 && (
          <p role="status" className="text-xs text-text-2">
            {selected.size} scenes selected
          </p>
        )}
        {allScenes.length > 0 && (
          <Timeline
            clips={clips}
            totalMs={allQuery.data?.totalDurationMs ?? 0}
            activeId={active?.id}
            playheadMs={playheadMs ?? active?.startMs}
            playing={playing}
            onSelect={onTimelineSelect}
            onPlayToggle={togglePlay}
            onPrev={() => move(-1)}
            onNext={() => move(1)}
          />
        )}
        {active && (
          <span className="sr-only" aria-live="polite">
            Scene {active.idx}: {active.pips.map((p) => `${PIP_LABELS[p.kind]} ${p.state}`).join(", ")}
          </span>
        )}
      </section>
      <SceneInspector
        episodeId={episodeId}
        scene={active}
        characters={characters.data?.items ?? []}
        styles={styles.data?.items ?? []}
        editingNarration={editingNarration}
        onEditingNarrationChange={setEditingNarration}
        previewAssetId={active ? renders.data?.items[0]?.report.scenePreviews?.[active.id] : undefined}
      />
      <ResplitConfirmDialog
        message={confirmSplit?.message}
        pending={split.isPending}
        onConfirm={() => confirmSplit && runSplit({ ...confirmSplit.body, discardWork: true })}
        onCancel={() => {
          setConfirmSplit(undefined);
          split.reset();
        }}
      />
    </div>
  );
}
