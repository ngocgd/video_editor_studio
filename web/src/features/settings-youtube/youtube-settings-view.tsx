import type { ReactNode } from "react";

import type { YouTubeChannel, YouTubeQuota } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { connectResultMessage, type YouTubeSettingsSearch } from "./connect-result";
import {
  useDisconnectYouTubeChannel,
  useIsTenantOwner,
  useStartYouTubeConnect,
  useUpdateYouTubeChannelAudit,
  useYouTubeChannels,
} from "./use-youtube-channels";
import { YouTubeChannelCard } from "./youtube-channel-card";

/** Today's units from the Google project's shared daily pool (resets at midnight Pacific time). */
export function QuotaMeter({ quota }: { quota: YouTubeQuota }) {
  const percent = quota.limit > 0 ? Math.min(100, Math.round((quota.used / quota.limit) * 100)) : 100;
  const resets = new Date(quota.resetsAt).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  return (
    <div className="flex flex-col gap-1">
      <div className="flex justify-between text-xs">
        <span>
          {quota.used.toLocaleString()} / {quota.limit.toLocaleString()} units used today
        </span>
        <span className="text-text-2">Resets {resets}</span>
      </div>
      <div
        role="progressbar"
        aria-label="YouTube API quota used today"
        aria-valuemin={0}
        aria-valuemax={quota.limit}
        aria-valuenow={quota.used}
        className="h-1.5 overflow-hidden rounded-full bg-well"
      >
        <div className={`h-full ${percent >= 90 ? "bg-warning" : "bg-primary"}`} style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}

export interface YouTubeSettingsPanelProps {
  channels: YouTubeChannel[];
  oauthConfigured: boolean;
  quota: YouTubeQuota;
  isOwner: boolean;
  search: YouTubeSettingsSearch;
  connecting?: boolean;
  onConnect: () => void;
  channelCard: (channel: YouTubeChannel) => ReactNode;
}

/** Presentational body of the page, kept free of queries so it can be tested directly. */
export function YouTubeSettingsPanel({ channels, oauthConfigured, quota, isOwner, search, connecting, onConnect, channelCard }: YouTubeSettingsPanelProps) {
  const result = connectResultMessage(search);
  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <h1 className="text-lg font-semibold">YouTube channels</h1>

      {result ? (
        <p role={result.tone === "error" ? "alert" : "status"} className={`rounded-md border px-3 py-2 text-sm ${result.tone === "error" ? "border-destructive/50 text-destructive" : "border-success/40 text-success"}`}>
          {result.text}
        </p>
      ) : null}

      {!oauthConfigured ? (
        <p role="note" className="rounded-md border border-warning/40 px-3 py-2 text-sm">
          Google OAuth is not configured on this server, so channels cannot be connected. An administrator sets GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET_PATH and GOOGLE_OAUTH_REDIRECT_URL.
        </p>
      ) : null}

      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h2 className="text-md font-medium">Connected channels</h2>
          {isOwner ? (
            <Button variant="primary" size="sm" disabled={!oauthConfigured || connecting} onClick={onConnect}>
              Connect a YouTube channel
            </Button>
          ) : null}
        </div>
        {!isOwner ? <p className="text-xs text-text-2">Only the workspace owner can connect or disconnect channels.</p> : null}
        {channels.length === 0 ? (
          <p className="text-sm text-text-2">No channel connected yet.</p>
        ) : (
          <div className="flex flex-col gap-3">{channels.map((channel) => channelCard(channel))}</div>
        )}
      </section>

      <section className="flex flex-col gap-2">
        <h2 className="text-md font-medium">API quota</h2>
        <QuotaMeter quota={quota} />
      </section>
    </div>
  );
}

/** Settings > YouTube: connect, list, audit status and disconnect of YouTube channels. */
export function YouTubeSettingsView({ search }: { search: YouTubeSettingsSearch }) {
  const { data, isError } = useYouTubeChannels();
  const isOwner = useIsTenantOwner();
  const connect = useStartYouTubeConnect();
  const updateAudit = useUpdateYouTubeChannelAudit();
  const disconnect = useDisconnectYouTubeChannel();

  if (isError) return <p className="text-sm text-destructive">The channel list could not be loaded.</p>;
  if (!data) return <p className="text-sm text-text-2">Loading…</p>;

  return (
    <YouTubeSettingsPanel
      channels={data.items}
      oauthConfigured={data.oauthConfigured}
      quota={data.quota}
      isOwner={isOwner}
      search={search}
      connecting={connect.isPending}
      onConnect={() => connect.mutate()}
      channelCard={(channel) => (
        <YouTubeChannelCard
          // Remount after a save so the form fields restart from the stored values.
          key={`${channel.id}:${channel.updatedAt}`}
          channel={channel}
          isOwner={isOwner}
          saving={updateAudit.isPending}
          disconnecting={disconnect.isPending}
          onSaveAudit={(body) => updateAudit.mutate({ id: channel.id, body })}
          onDisconnect={() => disconnect.mutate(channel.id)}
        />
      )}
    />
  );
}
