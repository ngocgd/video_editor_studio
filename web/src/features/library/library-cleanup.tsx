import { useState } from "react";

import { ApiError } from "../../api/client";
import type { CleanupPreview } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/ui/dialog";
import { formatBytes } from "../../lib/format";
import { errorMessage } from "../render/render-model";
import { previewSummary } from "./library-model";
import { useConfirmCleanup, usePreviewCleanup } from "./use-library";

/** The confirm step of a cleanup: what the dry run found, and the confirm or cancel choice. */
export function CleanupConfirmDialog({
  preview,
  notice,
  pending,
  onConfirm,
  onCancel,
}: {
  preview: CleanupPreview | undefined;
  notice?: string;
  pending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const empty = preview ? preview.segments.length + preview.takes.length === 0 : true;
  return (
    <Dialog open={preview !== undefined} onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogTitle>Clean up the library?</DialogTitle>
        <DialogDescription className="mt-2 text-sm text-text-2">
          {preview && (empty ? "Nothing has expired: no render cache entry or unselected take is past its retention." : `This deletes ${previewSummary(preview)}`)}
        </DialogDescription>
        {preview && !empty && (
          <p className="mt-2 text-xs text-text-2">
            Selected takes, finished renders, character references and the cache of the latest render stay. Render cache older than {preview.settings.segmentTtlDays} days and unselected takes older than{" "}
            {preview.settings.takeTtlDays} days go. The cleanup runs as an audited job.
          </p>
        )}
        {notice && (
          <p role="alert" className="mt-2 text-sm text-warning">
            {notice}
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" size="sm" disabled={pending || empty} onClick={onConfirm}>
            Delete {preview ? formatBytes(preview.bytes) : ""}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Manual cleanup: a dry run first, then a confirm that names exactly the
 * previewed set (by token). If the set changed in between (409), the
 * preview is taken again and shown with a notice instead of deleting.
 */
export function LibraryCleanup() {
  const previewCleanup = usePreviewCleanup();
  const confirm = useConfirmCleanup();
  const [preview, setPreview] = useState<CleanupPreview | undefined>();
  const [notice, setNotice] = useState<string | undefined>();
  const [started, setStarted] = useState<string | undefined>();

  const runPreview = (why?: string) =>
    previewCleanup.mutate(
      {},
      {
        onSuccess: (p) => {
          setPreview(p);
          setNotice(why);
        },
      },
    );

  return (
    <div className="flex flex-col gap-2">
      <Button variant="secondary" size="sm" disabled={previewCleanup.isPending} onClick={() => runPreview()}>
        {previewCleanup.isPending ? "Checking…" : "Preview cleanup"}
      </Button>
      {previewCleanup.error && (
        <p role="alert" className="text-xs text-destructive">
          {errorMessage(previewCleanup.error)}
        </p>
      )}
      {started && (
        <p role="status" className="text-xs text-text-2">
          {started}
        </p>
      )}
      <CleanupConfirmDialog
        preview={preview}
        notice={notice ?? (confirm.error && !(confirm.error instanceof ApiError && confirm.error.status === 409) ? errorMessage(confirm.error) : undefined)}
        pending={confirm.isPending || previewCleanup.isPending}
        onCancel={() => {
          setPreview(undefined);
          setNotice(undefined);
          confirm.reset();
        }}
        onConfirm={() =>
          preview &&
          confirm.mutate(
            { body: { token: preview.token } },
            {
              onSuccess: (r) => {
                setPreview(undefined);
                setNotice(undefined);
                setStarted(`Cleanup queued: ${r.segments} cache entries and ${r.takes} takes, ${formatBytes(r.bytes)}.`);
              },
              onError: (e) => {
                if (e instanceof ApiError && e.status === 409) runPreview("What can be deleted changed since the preview. Check the new list and confirm again.");
              },
            },
          )
        }
      />
    </div>
  );
}
