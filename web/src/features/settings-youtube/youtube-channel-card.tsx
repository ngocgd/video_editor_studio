import { useState } from "react";

import type { YouTubeChannel, YouTubeChannelAuditUpdate } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../../components/ui/dialog";
import { Input } from "../../components/ui/input";

const STATUS_LABEL: Record<YouTubeChannel["status"], string> = {
  connected: "Connected",
  reconnect_needed: "Reconnect needed",
  disconnected: "Disconnected",
};

const STATUS_CLASS: Record<YouTubeChannel["status"], string> = {
  connected: "border-success/40 text-success",
  reconnect_needed: "border-warning/40 text-warning",
  disconnected: "border-border text-text-2",
};

export function ChannelStatusChip({ status }: { status: YouTubeChannel["status"] }) {
  return <span className={`rounded-full border px-2 py-0.5 text-xs ${STATUS_CLASS[status]}`}>{STATUS_LABEL[status]}</span>;
}

/** Whether the channel may upload videos longer than 15 minutes (YouTube channel verification). */
export function longUploadsText(status: YouTubeChannel["longUploadsStatus"]): string {
  switch (status) {
    case "allowed":
      return "Videos longer than 15 minutes: allowed.";
    case "eligible":
      return "Videos longer than 15 minutes: eligible, but the channel must be verified on YouTube first.";
    case "disallowed":
      return "Videos longer than 15 minutes: not allowed for this channel.";
    default:
      return "Videos longer than 15 minutes: unknown until the channel is checked again.";
  }
}

/**
 * Until the Google API project passes YouTube's audit, YouTube locks every
 * API upload to private. The page says so plainly instead of offering a
 * public option that would silently not apply.
 */
export function visibilityText(channel: Pick<YouTubeChannel, "apiProjectAudited">): string {
  return channel.apiProjectAudited
    ? "Uploads may be public, unlisted or private."
    : "Private (API not audited): YouTube keeps every upload from this app private until the Google API project passes the audit.";
}

export interface YouTubeChannelCardProps {
  channel: YouTubeChannel;
  isOwner: boolean;
  saving?: boolean;
  disconnecting?: boolean;
  onSaveAudit: (body: YouTubeChannelAuditUpdate) => void;
  onDisconnect: () => void;
}

export function YouTubeChannelCard({ channel, isOwner, saving, disconnecting, onSaveAudit, onDisconnect }: YouTubeChannelCardProps) {
  const [audited, setAudited] = useState(channel.apiProjectAudited);
  const [formDate, setFormDate] = useState(channel.auditFormDate ?? "");
  const [note, setNote] = useState(channel.auditNote);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const disconnected = channel.status === "disconnected";
  const dirty = audited !== channel.apiProjectAudited || formDate !== (channel.auditFormDate ?? "") || note !== channel.auditNote;
  const titleId = `yt-channel-${channel.id}`;

  return (
    <article aria-labelledby={titleId} className="flex flex-col gap-3 rounded-lg border border-border bg-card p-4">
      <header className="flex items-center gap-3">
        {channel.thumbnailUrl ? (
          <img src={channel.thumbnailUrl} alt="" width={40} height={40} className="size-10 rounded-full" referrerPolicy="no-referrer" />
        ) : null}
        <div className="flex min-w-0 flex-1 flex-col">
          <h3 id={titleId} className="truncate text-sm font-medium">
            {channel.title}
          </h3>
          <span className="font-mono text-xs text-text-2">{channel.youtubeChannelId}</span>
        </div>
        <ChannelStatusChip status={channel.status} />
      </header>

      {channel.status === "reconnect_needed" ? (
        <p className="text-xs text-warning">Google no longer accepts this channel's authorization. Connect the channel again to resume uploads.</p>
      ) : null}

      <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-text-2">Permissions</dt>
        <dd className="break-all font-mono">{channel.scopes.length ? channel.scopes.join(" ") : "none"}</dd>
        <dt className="text-text-2">Uploads</dt>
        <dd>{channel.canUpload ? "Can upload" : "Cannot upload"}</dd>
        <dt className="text-text-2">Visibility</dt>
        <dd>{visibilityText(channel)}</dd>
        <dt className="text-text-2">Long videos</dt>
        <dd>{longUploadsText(channel.longUploadsStatus)}</dd>
        <dt className="text-text-2">Custom thumbnails</dt>
        <dd>{channel.customThumbnailsOk ? "Allowed" : "Not allowed (the channel must be verified)"}</dd>
      </dl>

      {isOwner && !disconnected ? (
        <form
          className="flex flex-col gap-2 border-t border-border pt-3"
          onSubmit={(e) => {
            e.preventDefault();
            onSaveAudit({ apiProjectAudited: audited, auditFormDate: formDate || undefined, auditNote: note });
          }}
        >
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={audited} onChange={(e) => setAudited(e.target.checked)} />
            Google API project passed the YouTube audit
          </label>
          <div className="grid grid-cols-2 gap-2">
            <label className="flex flex-col gap-1 text-xs">
              Audit form submitted on
              <Input type="date" value={formDate} onChange={(e) => setFormDate(e.target.value)} />
            </label>
            <label className="flex flex-col gap-1 text-xs">
              Note
              <Input value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} />
            </label>
          </div>
          <div className="flex justify-between gap-2">
            <Button type="submit" size="sm" disabled={!dirty || saving}>
              Save audit status
            </Button>
            <Button type="button" variant="destructive" size="sm" disabled={disconnecting} onClick={() => setConfirmOpen(true)}>
              Disconnect
            </Button>
          </div>
        </form>
      ) : null}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogTitle className="text-md font-medium">Disconnect {channel.title}?</DialogTitle>
          <DialogDescription className="mt-2 text-sm text-text-2">
            Loomtale revokes its access at Google and deletes the stored token. Videos already on YouTube stay there. You can connect the channel again later.
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            <Button size="sm" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                setConfirmOpen(false);
                onDisconnect();
              }}
            >
              Disconnect
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </article>
  );
}
