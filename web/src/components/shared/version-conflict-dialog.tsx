import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../ui/dialog";
import { Button } from "../ui/button";

/**
 * 409 (stale `expectedVersion`) merge prompt shared by the bible section
 * PATCH and the draft paragraph-ops PATCH (phase 6). No automatic
 * three-way merge: Reload discards the local edit and refetches the
 * server's current version; Discard mine keeps editing locally and lets
 * the next save retry (still against the old version, so it will conflict
 * again until the user reloads) -- kept intentionally simple per the
 * phase doc's scope.
 */
export function VersionConflictDialog({
  open,
  onReload,
  onDiscard,
}: {
  open: boolean;
  onReload: () => void;
  onDiscard: () => void;
}) {
  return (
    <Dialog open={open}>
      <DialogContent showClose={false} onEscapeKeyDown={(e) => e.preventDefault()} onInteractOutside={(e) => e.preventDefault()}>
        <DialogTitle>This changed elsewhere</DialogTitle>
        <DialogDescription className="mt-2 text-sm text-text-2">
          Someone (or another tab) saved a newer version of this content while you were editing. Reload the latest
          version, or keep editing your local copy.
        </DialogDescription>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onDiscard}>
            Keep editing mine
          </Button>
          <Button variant="primary" size="sm" onClick={onReload}>
            Reload latest
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
