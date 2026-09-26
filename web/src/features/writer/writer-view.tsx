import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { getEpisodeOptions } from "../../api/gen/@tanstack/react-query.gen";
import type { AiActionRequest, TargetLanguage } from "../../api/gen/types.gen";
import { VersionConflictDialog } from "../../components/shared/version-conflict-dialog";
import { Button } from "../../components/ui/button";
import { useMe } from "../auth/use-auth";
import { pushShortcutScope, popShortcutScope, useShortcut } from "../../lib/shortcuts";
import { AiToolbar, type ToolbarAction } from "./ai-toolbar";
import type { DiffParagraph } from "./paragraph-diff";
import { useAiAction } from "./use-ai-action";
import { useDraft } from "./use-draft";
import { WriterBeatsPanel } from "./writer-beats-panel";
import { WriterContextPanel } from "./writer-context-panel";
import { WriterEditor, type WriterSelection } from "./writer-editor";
import { WriterFooter } from "./writer-footer";

const NO_SELECTION: WriterSelection = { paragraphIds: [], text: "" };

function otherLang(lang: TargetLanguage): TargetLanguage {
  return lang === "en" ? "vi" : "en";
}

/** Three-pane episode editor (phase 6 priority screen): bible/context left, TipTap editor centre, beats/characters right. */
export function WriterView({ seriesId, episodeId }: { seriesId: string; episodeId: string }) {
  const { data: me } = useMe();
  const isViewer = me?.activeRole === "viewer";

  const [lang, setLang] = useState<TargetLanguage>("en");
  const episodeQuery = useQuery(getEpisodeOptions({ path: { id: episodeId } }));
  const draft = useDraft(episodeId, lang);
  const ai = useAiAction(episodeId);

  // Live paragraphs for the footer's word count: the draft's seed, then
  // every editor change. The editor owns its document once mounted; the
  // draft hook remounts it (a new seed key) when the document is replaced.
  const [paragraphs, setParagraphs] = useState<DiffParagraph[]>([]);
  const [selection, setSelection] = useState<WriterSelection>(NO_SELECTION);
  const [applyError, setApplyError] = useState<string | null>(null);
  const proposalContainerRef = useRef<HTMLDivElement | null>(null);
  // The last request, so Retry repeats it exactly (including a beat id).
  const lastRunRef = useRef<{ body: AiActionRequest; originalText: string } | null>(null);

  const toggleLang = useCallback(() => {
    setSelection(NO_SELECTION);
    setLang((l) => otherLang(l));
  }, []);

  useEffect(() => {
    if (draft.seed) setParagraphs(draft.seed.paragraphs);
  }, [draft.seed]);

  const { edit } = draft;
  const handleParagraphsChange = useCallback(
    (current: DiffParagraph[]) => {
      setParagraphs(current);
      if (!isViewer) edit(current);
    },
    [edit, isViewer],
  );

  const run = useCallback(
    (body: AiActionRequest, originalText: string) => {
      lastRunRef.current = { body, originalText };
      setApplyError(null);
      ai.run(body, originalText);
    },
    [ai],
  );

  const runAction = useCallback(
    (action: ToolbarAction, instruction?: string) => {
      const selected = selection.paragraphIds;
      if (action === "continue") {
        // Continue works from the caret when nothing is selected.
        const anchor = selected.length > 0 ? selected : selection.caretParagraphId ? [selection.caretParagraphId] : [];
        run({ action, lang, paragraphIds: anchor, instruction }, "");
        return;
      }
      if (action === "translate") {
        // The selection, or the whole draft when nothing is selected.
        run({ action, lang, paragraphIds: selected, instruction }, "");
        return;
      }
      if (selected.length === 0) return;
      run({ action, lang, paragraphIds: selected, instruction }, selection.text);
    },
    [lang, run, selection],
  );

  const acceptProposal = useCallback(async () => {
    // Only a finished proposal with text may be applied; Tab while the step
    // is still generating (or after it failed) does nothing.
    const proposal = ai.proposal;
    if (!proposal?.done) return;
    // The server applies the step's own text and taint: replacing the
    // selection, or adding after it for Continue/expand-beat, or appending
    // to the other language's draft for Translate.
    const target = proposal.action === "translate" ? otherLang(lang) : lang;
    const error = await draft.applyStep({ stepId: proposal.stepId, paragraphIds: proposal.paragraphIds }, target);
    if (error) {
      setApplyError(error);
      return;
    }
    setSelection(NO_SELECTION);
    ai.clear();
    if (target !== lang) setLang(target);
  }, [ai, draft, lang]);

  // Tab/Esc must work while the selection toolbar's contenteditable region
  // has focus, where the global shortcut registry deliberately ignores
  // plain (non-Ctrl) keys (see lib/shortcuts.ts), so these are handled
  // locally instead of through useShortcut.
  useEffect(() => {
    if (!ai.proposal) return;
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Tab") {
        event.preventDefault();
        void acceptProposal();
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

  const hasSelection = selection.paragraphIds.length > 0;
  // A proposal stays visible even before a draft exists (expand-beat on an
  // outlined episode creates the draft it writes into).
  const showToolbar = !isViewer && (ai.proposal !== null || (draft.seed !== null && (hasSelection || selection.caretParagraphId !== undefined)));

  return (
    <div className="grid h-[calc(100vh-140px)] grid-cols-[280px_1fr_360px] gap-0 rounded-md border border-border">
      <WriterContextPanel seriesId={seriesId} />

      <div className="flex min-h-0 flex-col border-x border-border">
        <div className="flex h-9 shrink-0 items-center gap-2 border-b border-border px-3 text-sm font-medium">
          {episode.title || "Untitled episode"}
          {isViewer && <span className="ml-auto rounded-sm bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">Read-only (viewer role)</span>}
        </div>

        {draft.seed ? (
          <WriterEditor
            key={`${lang}:${draft.seed.key}`}
            initialParagraphs={draft.seed.paragraphs}
            lang={lang}
            readOnly={isViewer}
            onParagraphsChange={handleParagraphsChange}
            onSelectionChange={setSelection}
          />
        ) : (
          // Never mount the editor before its draft has loaded: an empty
          // editor's first update would autosave over the stored paragraphs.
          <div className="flex-1 p-6 text-sm text-text-2">
            {draft.isLoading ? (
              "Loading draft…"
            ) : (
              <div className="flex flex-col items-start gap-3">
                <p>No {lang.toUpperCase()} draft for this episode yet.</p>
                {!isViewer && (
                  <Button variant="primary" size="sm" onClick={() => void draft.create()}>
                    Create {lang.toUpperCase()} draft
                  </Button>
                )}
              </div>
            )}
          </div>
        )}

        {showToolbar && (
          <div ref={proposalContainerRef} className="flex flex-col gap-2 border-t border-border p-3">
            {applyError && (
              <p role="alert" className="text-sm text-destructive">
                {applyError}
              </p>
            )}
            <AiToolbar
              disabled={ai.isStarting}
              disabledReason={ai.error ? "AI action failed; see the render queue." : undefined}
              hasSelection={hasSelection}
              onAction={runAction}
              proposal={ai.proposal}
              onAccept={() => void acceptProposal()}
              onReject={() => {
                setApplyError(null);
                ai.clear();
              }}
              onRetry={() => lastRunRef.current && run(lastRunRef.current.body, lastRunRef.current.originalText)}
            />
          </div>
        )}

        <WriterFooter paragraphs={paragraphs} lang={lang} saveStatus={draft.status} onToggleLang={toggleLang} />
      </div>

      <WriterBeatsPanel outline={episode.outline} onExpandBeat={(beatId) => run({ action: "expand_beat", lang, beatId }, "")} />

      <VersionConflictDialog open={draft.conflict} onReload={() => void draft.reloadLatest()} onDiscard={() => void draft.keepMine()} />
    </div>
  );
}
