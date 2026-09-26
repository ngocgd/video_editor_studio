import { useState } from "react";
import { WandSparkles } from "lucide-react";

import { DiffProposal } from "../../components/shared/diff-proposal";
import { Kbd } from "../../components/shared/kbd";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import type { AiActionRequest } from "../../api/gen/types.gen";
import type { AiProposal } from "./use-ai-action";

const MAX_INSTRUCTION_LENGTH = 500;

type ToolbarAction = Extract<AiActionRequest["action"], "rewrite" | "expand" | "shorten" | "continue">;

const ACTIONS: { action: ToolbarAction; label: string; keys?: string }[] = [
  { action: "rewrite", label: "Rewrite", keys: "Ctrl⇧R" },
  { action: "expand", label: "Expand" },
  { action: "shorten", label: "Shorten" },
  { action: "continue", label: "Continue", keys: "Ctrl↵" },
];

/**
 * Quiet floating toolbar on text selection (guidelines §7): Rewrite / Expand
 * / Shorten / Continue plus an optional instruction. Simplification (noted
 * in the phase report): rendered docked below the selection rather than as
 * inline ProseMirror decorations, to ship correctness within the phase's
 * time budget; the phase doc explicitly allows this trade-off.
 */
export function AiToolbar({
  disabled,
  disabledReason,
  onAction,
  proposal,
  onAccept,
  onReject,
  onRetry,
}: {
  disabled: boolean;
  disabledReason?: string;
  onAction: (action: ToolbarAction, instruction?: string) => void;
  proposal: AiProposal | null;
  onAccept: () => void;
  onReject: () => void;
  onRetry: () => void;
}) {
  const [instruction, setInstruction] = useState("");

  if (proposal?.error) {
    return (
      <div role="alert" className="flex items-center justify-between gap-2 rounded-md border border-border bg-card p-3 text-sm">
        <span className="text-destructive">{proposal.error}</span>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={onRetry}>
            Retry
          </Button>
          <Button variant="ghost" size="sm" onClick={onReject}>
            Dismiss <Kbd>Esc</Kbd>
          </Button>
        </div>
      </div>
    );
  }

  if (proposal && !proposal.done) {
    return (
      <div role="status" className="flex items-center justify-between gap-2 rounded-md border border-border bg-card p-3 text-sm text-text-2">
        <span>Generating…</span>
        <Button variant="ghost" size="sm" onClick={onReject}>
          Dismiss <Kbd>Esc</Kbd>
        </Button>
      </div>
    );
  }

  if (proposal) {
    return <DiffProposal segments={proposal.segments} provider={proposal.provider} onAccept={onAccept} onReject={onReject} onRetry={onRetry} />;
  }

  return (
    <div role="toolbar" aria-label="AI actions on selection" className="flex flex-col gap-2 rounded-md border border-border bg-popover p-2 shadow-[var(--shadow-overlay)]">
      <div className="flex items-center gap-1">
        <WandSparkles size={14} strokeWidth={1.75} aria-hidden="true" className="text-primary-text" />
        {ACTIONS.map((a) => (
          <Button key={a.action} variant="ghost" size="sm" disabled={disabled} onClick={() => onAction(a.action, instruction.trim() || undefined)}>
            {a.label}
            {a.keys && <Kbd>{a.keys}</Kbd>}
          </Button>
        ))}
      </div>
      <Input
        placeholder="Instruction (optional, e.g. 'make it tenser')"
        value={instruction}
        maxLength={MAX_INSTRUCTION_LENGTH}
        onChange={(e) => setInstruction(e.target.value.slice(0, MAX_INSTRUCTION_LENGTH))}
      />
      {disabled && disabledReason && <p className="text-xs text-warning">{disabledReason}</p>}
    </div>
  );
}

export type { ToolbarAction };
