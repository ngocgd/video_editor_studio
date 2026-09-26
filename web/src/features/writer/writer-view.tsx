import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { getEpisodeOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { TargetLanguage } from "../../api/gen/types.gen";
import { VersionConflictDialog } from "../../components/shared/version-conflict-dialog";
import { useMe } from "../auth/use-auth";
import { pushShortcutScope, popShortcutScope, useShortcut } from "../../lib/shortcuts";
import { AiToolbar, type ToolbarAction } from "./ai-toolbar";
import type { DiffParagraph } from "./paragraph-diff";
import { diffParagraphs } from "./paragraph-diff";
import { useAiAction } from "./use-ai-action";
import { useDraft } from "./use-draft";
import { WriterBeatsPanel } from "./writer-beats-panel";
import { WriterContextPanel } from "./writer-context-panel";
import { WriterEditor, type WriterSelection } from "./writer-editor";
import { WriterFooter } from "./writer-footer";

/** Three-pane episode editor (phase 6 priority screen): bible/context left, TipTap editor centre, beats/characters right. */
export function WriterView({ seriesId, episodeId }: { seriesId: string; episodeId: string }) {
  const { data: me } = useMe();
  const isViewer = me?.activeRole === "viewer";

  const [lang, setLang] = useState<TargetLanguage>("en");
  const episodeQuery = useQuery(getEpisodeOptions({ path: { id: episodeId } }));
  const draft = useDraft(episodeId, lang);
  const ai = useAiAction(episodeId);

  const [paragraphs, setParagraphs] = useState<DiffParagraph[]>([]);
  const [selection, setSelection] = useState<WriterSelection>({ paragraphIds: [], text: "" });
  const proposalContainerRef = useRef<HTMLDivElement | null>(null);
  // The editor owns its document once mounted, so replacing that document
  // means remounting it: `editorGeneration` is part of its key, and
  // `seedOverride` (an accepted AI proposal) takes precedence over the
  // loaded draft as its initial content.
  const [editorGeneration, setEditorGeneration] = useState(0);
  const [seedOverride, setSeedOverride] = useState<DiffParagraph[] | null>(null);

  const toggleLang = useCallback(() => {
    setSeedOverride(null);
    setSelection({ paragraphIds: [], text: "" });
    setLang((l) => (l === "en" ? "vi" : "en"));
  }, []);

  const reloadAfterConflict = useCallback(async () => {
    setSeedOverride(null);
    await draft.reloadAfterConflict();
    setEditorGeneration((g) => g + 1);
  }, [draft]);

  useEffect(() => {
    if (draft.draft) setParagraphs(draft.draft.paragraphs.map((p) => ({ id: p.id, text: p.text })));
  }, [draft.draft]);

  const handleParagraphsChange = useCallback(
    (current: DiffParagraph[]) => {
      setParagraphs(current);
      if (!isViewer) draft.scheduleSave(current);
    },
    [draft, isViewer],
  );

  const runAction = useCallback(
    (action: ToolbarAction, instruction?: string) => {
      if (selection.paragraphIds.length === 0) return;
      ai.run({ action, lang, paragraphIds: selection.paragraphIds, instruction }, selection.text);
    },
    [ai, lang, selection],
  );

  const acceptProposal = useCallback(() => {
    // Only a finished proposal with text may replace the selection; Tab while
    // the step is still generating (or after it failed) must not blank it.
    if (!ai.proposal?.done) return;
    const { paragraphIds, text } = ai.proposal;
    // Simplification (see phase report): a multi-paragraph selection is
    // replaced as one joined paragraph rather than re-split per original
    // paragraph boundary; single-paragraph actions (the common case) are exact.
    const next = paragraphs.map((p) => (p.id === paragraphIds[0] ? { ...p, text } : p)).filter((p) => !paragraphIds.slice(1).includes(p.id));
    setParagraphs(next);
    setSeedOverride(next);
    setEditorGeneration((g) => g + 1);
    setSelection({ paragraphIds: [], text: "" });
    draft.applyOpsNow(diffParagraphs(paragraphs, next));
    ai.clear();
  }, [ai, draft, paragraphs]);

  // Tab/Esc must work while the selection toolbar's contenteditable region
  // has focus, where the global shortcut registry deliberately ignores
  // plain (non-Ctrl) keys (see lib/shortcuts.ts), so these are handled
  // locally instead of through useShortcut.
  useEffect(() => {
    if (!ai.proposal) return;
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Tab") {
        event.preventDefault();
        acceptProposal();
      } else if (event.key === "Escape") {
        event.preventDefault();
        ai.clear();
      }
    };
    window.addEventListener("keydown", handler, true);
    return () => window.removeEventListener("keydown", handler, true);
  }, [ai, acceptProposal]);

  useEffect(() => {
    pushShortcutScope("writer");
    return () => popShortcutScope("writer");
  }, []);

  useShortcut("writer", "ctrl+enter", () => !isViewer && runAction("continue"));
  useShortcut("writer", "ctrl+shift+r", () => !isViewer && runAction("rewrite"));
  useShortcut("writer", "ctrl+shift+e", () => !isViewer && selection.paragraphIds.length > 0 && runAction("expand"));
  useShortcut("writer", "ctrl+alt+l", toggleLang);

  const episode = episodeQuery.data;
  if (!episode) return <p className="p-4 text-sm text-text-2">Loading…</p>;

  return (
    <div className="grid h-[calc(100vh-140px)] grid-cols-[280px_1fr_360px] gap-0 rounded-md border border-border">
      <WriterContextPanel seriesId={seriesId} />

      <div className="flex min-h-0 flex-col border-x border-border">
        <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border px-3 text-sm font-medium">
          {episode.title || "Untitled episode"}
          {isViewer && <span className="ml-auto rounded-sm bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">Read-only (viewer role)</span>}
        </div>

        {draft.draft ? (
          <WriterEditor
            key={`${lang}:${editorGeneration}`}
            initialParagraphs={seedOverride ?? draft.draft.paragraphs.map((p) => ({ id: p.id, text: p.text }))}
            lang={lang}
            readOnly={isViewer}
            onParagraphsChange={handleParagraphsChange}
            onSelectionChange={setSelection}
          />
        ) : (
          // Never mount the editor before its draft has loaded: an empty
          // editor's first update would autosave over the stored paragraphs.
          <p className="flex-1 p-6 text-sm text-text-2">{draft.isLoading ? "Loading draft…" : `No ${lang.toUpperCase()} draft for this episode yet.`}</p>
        )}

        {!isViewer && (selection.paragraphIds.length > 0 || ai.proposal) && (
          <div ref={proposalContainerRef} className="border-t border-border p-3">
            <AiToolbar
              disabled={ai.isStarting}
              disabledReason={ai.error ? "AI action failed; see the render queue." : undefined}
              onAction={runAction}
              proposal={ai.proposal}
              onAccept={acceptProposal}
              onReject={() => ai.clear()}
              onRetry={() => runAction(ai.proposal?.action as ToolbarAction)}
            />
          </div>
        )}

        <WriterFooter paragraphs={paragraphs} lang={lang} saving={draft.saving} onToggleLang={toggleLang} />
      </div>

      <WriterBeatsPanel outline={episode.outline} onExpandBeat={(beatId) => ai.run({ action: "expand_beat", lang, beatId }, "")} />

      <VersionConflictDialog open={draft.conflict} onReload={() => void reloadAfterConflict()} onDiscard={draft.dismissConflict} />
    </div>
  );
}
