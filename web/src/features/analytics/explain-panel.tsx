import { Sparkles } from "lucide-react";
import { useState } from "react";

import { InlineError } from "../../components/shared/inline-error";
import { Button } from "../../components/ui/button";
import type { AnalyticsExplanation } from "../../api/gen/types.gen";
import { useAnalyticsExplanation, useCanEditAnalytics, useExplainChannel } from "./use-analytics";

export function ExplanationResult({ explanation }: { explanation: AnalyticsExplanation }) {
  if (explanation.status === "failed" || explanation.status === "canceled") {
    return <InlineError cause={explanation.error ? `The explanation failed: ${explanation.error}` : "The explanation did not finish."} />;
  }
  if (explanation.status !== "done") {
    return (
      <p role="status" className="text-sm text-text-2">
        Writing the explanation ({explanation.status})...
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      {/* Model output is untrusted: rendered as plain text, never as HTML or Markdown. */}
      <p className="whitespace-pre-wrap text-sm text-foreground">{explanation.text ?? ""}</p>
      <p className="text-2xs text-text-2">
        {[explanation.provider, explanation.model].filter(Boolean).join(" / ")}
        {explanation.costUsd !== undefined && ` · $${explanation.costUsd.toFixed(4)}`}
      </p>
    </div>
  );
}

/**
 * "Explain" asks the tenant's LLM to describe the last 28 days. Only the
 * channel aggregates (numbers and video ids, no titles) are sent; the step
 * runs in the worker and this panel polls it until it finishes.
 */
export function ExplainPanel({ channelId }: { channelId: string }) {
  const [explanationId, setExplanationId] = useState<string>();
  const explain = useExplainChannel();
  const explanation = useAnalyticsExplanation(explanationId);
  const canEdit = useCanEditAnalytics();
  const running = explanation.data !== undefined && !["done", "failed", "canceled"].includes(explanation.data.status);

  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs text-text-2">Sends only the channel totals, the previous period and per-video numbers (no titles) to your configured LLM.</p>
      {canEdit && (
        <Button
          size="sm"
          className="self-start"
          disabled={explain.isPending || running}
          onClick={() => explain.mutate(channelId, { onSuccess: (data) => setExplanationId(data.id) })}
        >
          <Sparkles aria-hidden="true" />
          Explain the last 28 days
        </Button>
      )}
      {explanation.isError && <InlineError cause="Could not load the explanation." onRetry={() => void explanation.refetch()} />}
      {explanation.data && <ExplanationResult explanation={explanation.data} />}
    </div>
  );
}
