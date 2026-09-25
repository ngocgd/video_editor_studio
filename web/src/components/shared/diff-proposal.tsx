import { Button } from "../ui/button";
import { Kbd } from "./kbd";

export interface DiffSegment {
  text: string;
  op: "keep" | "insert" | "remove";
}

/**
 * AI result review (guidelines §7): green insert / struck removal, never
 * auto-replaces user text. Rendered as text nodes only (no HTML sinks), so
 * `segments` must already be plain-text pieces, not markup.
 */
export function DiffProposal({
  segments,
  provider,
  costLabel,
  onAccept,
  onReject,
  onRetry,
}: {
  segments: DiffSegment[];
  provider?: string;
  costLabel?: string;
  onAccept: () => void;
  onReject: () => void;
  onRetry?: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-border bg-card p-3">
      <p className="whitespace-pre-wrap text-sm leading-relaxed">
        {segments.map((segment, index) => {
          if (segment.op === "insert") {
            return (
              <ins key={index} className="bg-success-muted text-success no-underline">
                {segment.text}
              </ins>
            );
          }
          if (segment.op === "remove") {
            return (
              <del key={index} className="bg-destructive-muted text-destructive">
                {segment.text}
              </del>
            );
          }
          return <span key={index}>{segment.text}</span>;
        })}
      </p>
      <div className="flex items-center justify-between text-xs text-text-2">
        <div className="flex items-center gap-2">
          <Button variant="primary" size="sm" onClick={onAccept}>
            Accept <Kbd>Tab</Kbd>
          </Button>
          <Button variant="ghost" size="sm" onClick={onReject}>
            Reject <Kbd>Esc</Kbd>
          </Button>
          {onRetry && (
            <Button variant="ghost" size="sm" onClick={onRetry}>
              Retry
            </Button>
          )}
        </div>
        {(provider || costLabel) && (
          <span>
            {provider}
            {provider && costLabel ? " · " : ""}
            {costLabel}
          </span>
        )}
      </div>
    </div>
  );
}
