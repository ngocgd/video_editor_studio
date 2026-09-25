/**
 * Fans out `llm.delta` SSE frames (AI action token streaming, phase 6) to
 * per-step listeners in arrival order. Unlike step progress/status events
 * (see ./sse-cache), delta chunks are never coalesced or version-gated:
 * every chunk carries a slice of the proposal text and must be applied in
 * sequence, not just-the-latest.
 *
 * CONTRACT GAP (see the phase report): `openapi/paths/episodes.yaml`
 * documents createAiAction's response only as "tokens stream over SSE as
 * llm.delta events" and `openapi/paths/events.yaml` only formally specifies
 * the "step" event shape (progress/status, no text payload). No schema for
 * the delta payload itself exists anywhere in openapi/. This shape --
 * `{ stepId, text, done }`, with `text` the incremental chunk and `done`
 * marking the final frame -- is this client's assumption, mirroring the
 * `StepEvent` naming convention (api/internal/sse.Event). If the backend
 * emits a different shape, only this file and sse-bridge.ts's "llm.delta"
 * case need to change.
 */
export interface AiDeltaEvent {
  stepId: string;
  text: string;
  done: boolean;
}

export function isAiDeltaEvent(value: unknown): value is AiDeltaEvent {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as AiDeltaEvent).stepId === "string" &&
    typeof (value as AiDeltaEvent).text === "string" &&
    typeof (value as AiDeltaEvent).done === "boolean"
  );
}

export class AiDeltaBus {
  private readonly listeners = new Map<string, Set<(evt: AiDeltaEvent) => void>>();

  subscribe(stepId: string, listener: (evt: AiDeltaEvent) => void): () => void {
    let set = this.listeners.get(stepId);
    if (!set) {
      set = new Set();
      this.listeners.set(stepId, set);
    }
    set.add(listener);
    return () => {
      set?.delete(listener);
      if (set?.size === 0) this.listeners.delete(stepId);
    };
  }

  publish(evt: AiDeltaEvent): void {
    for (const listener of this.listeners.get(evt.stepId) ?? []) listener(evt);
  }
}
