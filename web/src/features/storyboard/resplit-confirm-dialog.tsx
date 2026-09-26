import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/ui/dialog";

/**
 * Confirmation for a re-split the API refused (409) because it would
 * delete scenes a person edited or scenes with takes. The server's
 * message carries the counts; nothing is deleted unless the user
 * confirms, which re-sends the split with `discardWork`.
 */
export function ResplitConfirmDialog({
  message,
  pending,
  onConfirm,
  onCancel,
}: {
  message: string | undefined;
  pending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <Dialog open={message !== undefined} onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogTitle>Delete edited scenes?</DialogTitle>
        <DialogDescription className="mt-2 text-sm text-text-2">{message}</DialogDescription>
        <p className="mt-2 text-sm text-text-2">
          Edited narration, speakers, prompts and every take of those scenes are lost. Scenes whose text is unchanged keep
          their work.
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Keep my scenes
          </Button>
          <Button variant="destructive" size="sm" disabled={pending} onClick={onConfirm}>
            Split and delete
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
