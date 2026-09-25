/** Comma-separated run-id topics per stream (api/internal/pipelineapi/events.go maxTopicsPerStream). */
export const MAX_TOPICS_PER_STREAM = 50;

/**
 * Tracks every tab's requested topic set (keyed by tab id) so the leader
 * can compute the union once, capped to the server's per-stream limit; the
 * excess relies on each screen's own polling fallback rather than ever
 * exceeding the cap and getting the whole stream 403'd.
 */
export class TopicRegistry {
  private readonly tabTopics = new Map<string, Set<string>>();

  set(tabId: string, topics: string[]): void {
    this.tabTopics.set(tabId, new Set(topics));
  }

  delete(tabId: string): void {
    this.tabTopics.delete(tabId);
  }

  unionCapped(): string[] {
    const set = new Set<string>();
    for (const topics of this.tabTopics.values()) {
      for (const topic of topics) set.add(topic);
    }
    return [...set].slice(0, MAX_TOPICS_PER_STREAM);
  }
}
