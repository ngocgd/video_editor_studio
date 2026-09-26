import { InlineError } from "../../components/shared/inline-error";
import { Button } from "../../components/ui/button";
import type { AnalyticsSuggestion } from "../../api/gen/types.gen";
import { evidenceLabel, formatEvidenceValue } from "./analytics-format";
import { useAnalyticsSuggestions, useCanEditAnalytics, useDismissSuggestion } from "./use-analytics";

export function SuggestionList({
  items,
  onDismiss,
  showVideo = true,
}: {
  items: AnalyticsSuggestion[];
  onDismiss?: (item: AnalyticsSuggestion) => void;
  showVideo?: boolean;
}) {
  if (items.length === 0) {
    return <p className="text-sm text-text-2">No suggestions right now. Rules are re-checked after every sync.</p>;
  }
  return (
    <ul className="flex flex-col gap-3">
      {items.map((item) => (
        <li key={`${item.videoId}/${item.rule}`} className="flex flex-col gap-2 rounded-md border border-border bg-card p-3">
          <div className="flex items-start justify-between gap-3">
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium text-foreground">{item.title}</span>
              <span className="text-2xs text-text-2">
                {showVideo && (item.videoId ? `Video ${item.videoId} · ` : "Whole channel · ")}rule {item.rule} v{item.version}
              </span>
            </div>
            {onDismiss && (
              <Button size="sm" variant="ghost" onClick={() => onDismiss(item)} aria-label={`Dismiss suggestion: ${item.title}`}>
                Dismiss
              </Button>
            )}
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
            {Object.entries(item.evidence).map(([key, value]) => (
              <div key={key} className="contents">
                <dt className="text-text-2">{evidenceLabel(key)}</dt>
                <dd className="font-mono tabular-nums text-foreground">{formatEvidenceValue(value)}</dd>
              </div>
            ))}
          </dl>
        </li>
      ))}
    </ul>
  );
}

/** Rule-based suggestions for a channel (or one video), each listing the evidence that triggered it. */
export function SuggestionsPanel({ channelId, videoId }: { channelId: string; videoId?: string }) {
  const suggestions = useAnalyticsSuggestions(channelId, videoId);
  const dismiss = useDismissSuggestion(channelId);
  const canEdit = useCanEditAnalytics();

  if (suggestions.isError) {
    return <InlineError cause="Could not load the suggestions." onRetry={() => void suggestions.refetch()} />;
  }
  if (!suggestions.data) {
    return <div className="h-20 animate-pulse rounded-md bg-muted motion-reduce:animate-none" aria-hidden="true" />;
  }
  return (
    <SuggestionList
      items={suggestions.data.items}
      showVideo={!videoId}
      onDismiss={canEdit ? (item) => dismiss.mutate({ videoId: item.videoId, rule: item.rule, dismissed: true }) : undefined}
    />
  );
}
