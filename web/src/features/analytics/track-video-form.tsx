import { useState } from "react";

import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { useTrackVideo } from "./use-analytics";

/** YouTube video ids are 11 characters; URLs are checked by the server, which accepts watch, youtu.be, shorts, embed and live links. */
export function looksLikeVideoRef(value: string): boolean {
  const trimmed = value.trim();
  return /^[A-Za-z0-9_-]{11}$/.test(trimmed) || /^https?:\/\//i.test(trimmed) || /^(www\.|m\.)?(youtube\.com|youtu\.be)\//i.test(trimmed);
}

/** Tracks an already published video (not uploaded by Loomtale) on the selected channel. */
export function TrackVideoForm({ channelId }: { channelId: string }) {
  const [value, setValue] = useState("");
  const track = useTrackVideo();
  const valid = looksLikeVideoRef(value);

  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (!valid) return;
        track.mutate({ channelId, video: value.trim() }, { onSuccess: () => setValue("") });
      }}
    >
      <div className="flex min-w-64 flex-1 flex-col gap-1">
        <label htmlFor="analytics-track-video" className="text-xs text-text-2">
          Track a published video (URL or video id)
        </label>
        <Input
          id="analytics-track-video"
          value={value}
          maxLength={512}
          placeholder="https://www.youtube.com/watch?v=..."
          onChange={(event) => setValue(event.target.value)}
        />
      </div>
      <Button type="submit" size="md" variant="primary" disabled={!valid || track.isPending}>
        Track video
      </Button>
    </form>
  );
}
