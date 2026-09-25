import { CircleX } from "lucide-react";

import { Button } from "../ui/button";

/** Inline error: cause in plain words + mono log excerpt + Retry/Open log (guidelines §7). */
export function InlineError({
  cause,
  logExcerpt,
  onRetry,
  onOpenLog,
}: {
  cause: string;
  logExcerpt?: string;
  onRetry?: () => void;
  onOpenLog?: () => void;
}) {
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-md border border-destructive/40 bg-destructive-muted p-3">
      <div className="flex items-start gap-2 text-sm text-destructive">
        <CircleX size={16} strokeWidth={1.75} className="mt-0.5 shrink-0" aria-hidden="true" />
        <p>{cause}</p>
      </div>
      {logExcerpt && (
        <pre className="max-h-32 overflow-auto rounded-sm bg-well p-2 font-mono text-xs text-text-2">{logExcerpt}</pre>
      )}
      {(onRetry || onOpenLog) && (
        <div className="flex gap-2">
          {onRetry && (
            <Button variant="secondary" size="sm" onClick={onRetry}>
              Retry
            </Button>
          )}
          {onOpenLog && (
            <Button variant="ghost" size="sm" onClick={onOpenLog}>
              Open log
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
