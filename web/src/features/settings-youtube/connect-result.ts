/**
 * The OAuth callback always redirects back to /settings/youtube with
 * ?connect=ok&channel=<id> or ?connect=error&reason=<fixed code>. Only the
 * known codes are mapped to text; nothing from the query string is ever
 * rendered verbatim.
 */
export interface YouTubeSettingsSearch {
  connect?: "ok" | "error";
  reason?: string;
  channel?: string;
}

export function validateYouTubeSettingsSearch(search: Record<string, unknown>): YouTubeSettingsSearch {
  const out: YouTubeSettingsSearch = {};
  if (search.connect === "ok" || search.connect === "error") out.connect = search.connect;
  if (typeof search.reason === "string" && /^[a-z_]{1,40}$/.test(search.reason)) out.reason = search.reason;
  if (typeof search.channel === "string" && /^[0-9a-f-]{36}$/i.test(search.channel)) out.channel = search.channel;
  return out;
}

const REASON_TEXT: Record<string, string> = {
  consent_denied: "Google consent was declined, so no channel was connected.",
  invalid_request: "Google returned an incomplete response. Start the connection again.",
  invalid_state: "The connect link expired or was opened in another session. Start the connection again from this page.",
  not_configured: "Google OAuth is not configured on this server.",
  exchange_failed: "Google did not accept the authorization. Start the connection again.",
  upload_scope_missing: "The YouTube upload permission was not granted. Connect again and allow uploads.",
  no_channel: "The Google account has no YouTube channel. Create a channel first, then connect again.",
  quota_exceeded: "Today's YouTube API quota is used up. Try again after the daily reset.",
  channel_read_failed: "The channel details could not be read from YouTube. Try again.",
  store_failed: "The channel could not be saved. Try again.",
};

export type ConnectResult = { tone: "success" | "error"; text: string } | null;

export function connectResultMessage(search: YouTubeSettingsSearch): ConnectResult {
  if (search.connect === "ok") return { tone: "success", text: "YouTube channel connected." };
  if (search.connect === "error") {
    return { tone: "error", text: (search.reason && REASON_TEXT[search.reason]) || "Connecting the channel failed. Try again." };
  }
  return null;
}
