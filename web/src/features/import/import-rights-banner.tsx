import { TriangleAlert, X } from "lucide-react";
import { useState } from "react";

/**
 * Persistent, dismissible-but-reappearing rights notice (phase 6 contract
 * §7): informational only, never blocks. "Reappearing" is implemented as
 * "dismissed only for this browser session", not permanently, so it still
 * surfaces again on a fresh visit.
 */
export function ImportRightsBanner() {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed) return null;

  return (
    <div role="status" className="flex items-start gap-2 rounded-md border border-warning bg-warning-muted p-3 text-sm text-warning">
      <TriangleAlert size={16} strokeWidth={1.75} aria-hidden="true" className="mt-0.5 shrink-0" />
      <p className="flex-1">
        This project includes text imported from user-supplied source material. Ensure you have the rights to use it.
      </p>
      <button type="button" aria-label="Dismiss" onClick={() => setDismissed(true)} className="shrink-0 rounded-sm p-0.5 hover:bg-accent">
        <X size={14} strokeWidth={1.75} aria-hidden="true" />
      </button>
    </div>
  );
}
