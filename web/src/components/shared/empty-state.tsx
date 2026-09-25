import type { ReactNode } from "react";

import { Button } from "../ui/button";

/** One sentence + one primary action, no illustrations (guidelines §7). */
export function EmptyState({
  message,
  actionLabel,
  onAction,
  icon,
}: {
  message: string;
  actionLabel?: string;
  onAction?: () => void;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 rounded-md border border-dashed border-border bg-well p-8 text-center">
      {icon}
      <p className="max-w-sm text-sm text-text-2">{message}</p>
      {actionLabel && onAction && (
        <Button variant="secondary" size="sm" onClick={onAction}>
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
