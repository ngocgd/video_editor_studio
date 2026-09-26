/**
 * Pure selectors shared by the Analytics page and the Dashboard widgets. Kept
 * free of components so the Dashboard chunk does not pull in the page.
 */
import type { AnalyticsChannelDay, YouTubeChannel } from "../../api/gen/types.gen";

export interface AnalyticsSearch {
  channel?: string;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Keeps only a well-formed channel id from the query string. */
export function validateAnalyticsSearch(search: Record<string, unknown>): AnalyticsSearch {
  return typeof search.channel === "string" && UUID.test(search.channel) ? { channel: search.channel } : {};
}

/** Channels with analytics: connected ones, plus ones that need reconnecting (their synced data stays readable). */
export function analyticsChannels(channels: YouTubeChannel[]): YouTubeChannel[] {
  return channels.filter((c) => c.status !== "disconnected");
}

/** Picks the channel from the query string when it is one of the tenant's, else the first. */
export function pickChannel(channels: YouTubeChannel[], requested: string | undefined): YouTubeChannel | undefined {
  return channels.find((c) => c.id === requested) ?? channels[0];
}

export interface WindowTotals {
  views?: number;
  watchHours?: number;
  subscribersNet?: number;
}

/** Sums a window; a metric missing on every day stays undefined (shown as not available), never 0. */
export function windowTotals(days: AnalyticsChannelDay[]): WindowTotals {
  const sum = (pick: (day: AnalyticsChannelDay) => number | undefined): number | undefined => {
    const present = days.map(pick).filter((v): v is number => v !== undefined);
    return present.length === 0 ? undefined : present.reduce((a, b) => a + b, 0);
  };
  const gained = sum((d) => d.subscribersGained);
  const lost = sum((d) => d.subscribersLost);
  return {
    views: sum((d) => d.views),
    watchHours: sum((d) => d.watchHours),
    subscribersNet: gained === undefined ? undefined : gained - (lost ?? 0),
  };
}
