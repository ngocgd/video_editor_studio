import type { QueryClient } from "@tanstack/react-query";

import { streamEvents } from "./gen/sdk.gen";
import { SseCachePatcher, type StepEvent } from "./sse-cache";
import { broadcastChannelSupported, CHANNEL_NAME, electLeader } from "./sse-leader";

type ChannelMessage =
  | { kind: "subscribe"; tabId: string; topics: string[] }
  | { kind: "unsubscribe"; tabId: string }
  | { kind: "leader-ready" }
  | { kind: "event"; event: string; data: unknown }
  | { kind: "resync" };

const TAB_ID = crypto.randomUUID();
const REBUILD_DEBOUNCE_MS = 300;

/**
 * The single realtime path (guidelines "reuse points"): one leader tab owns
 * the actual SSE connection and fans events out over a BroadcastChannel so
 * every tab, including the leader, stays under the server's per-user cap of
 * 6 concurrent streams (RT#2). Every tab runs its own bridge instance; only
 * the leader's instance opens a network connection.
 */
export class SseBridge {
  private readonly queryClient: QueryClient;
  private readonly channel: BroadcastChannel | null;
  private readonly patcher: SseCachePatcher;
  private readonly consumers = new Map<string, Set<string>>();
  private readonly tabTopics = new Map<string, Set<string>>();
  private readonly readyListeners = new Set<() => void>();
  private rebuildTimer: ReturnType<typeof setTimeout> | null = null;
  private isLeader = false;
  private ready = false;
  private streamAbort: AbortController | null = null;
  private currentTopicsKey = "";

  constructor(queryClient: QueryClient) {
    this.queryClient = queryClient;
    this.patcher = new SseCachePatcher(queryClient);
    this.channel = broadcastChannelSupported() ? new BroadcastChannel(CHANNEL_NAME) : null;
    this.channel?.addEventListener("message", (event: MessageEvent<ChannelMessage>) => {
      this.onChannelMessage(event.data);
    });
    electLeader(() => this.becomeLeader());
  }

  isReady(): boolean {
    return this.ready;
  }

  onReady(listener: () => void): () => void {
    this.readyListeners.add(listener);
    return () => this.readyListeners.delete(listener);
  }

  /** Registers a consumer's topic set (pipeline run ids); returns an unsubscribe function. */
  subscribe(consumerId: string, topics: string[]): () => void {
    this.consumers.set(consumerId, new Set(topics));
    this.publishOwnTopics();
    return () => {
      this.consumers.delete(consumerId);
      this.publishOwnTopics();
    };
  }

  private ownTopicUnion(): string[] {
    const set = new Set<string>();
    for (const topics of this.consumers.values()) {
      for (const topic of topics) set.add(topic);
    }
    return [...set];
  }

  private publishOwnTopics(): void {
    const topics = this.ownTopicUnion();
    this.tabTopics.set(TAB_ID, new Set(topics));
    this.channel?.postMessage({ kind: "subscribe", tabId: TAB_ID, topics } satisfies ChannelMessage);
    if (this.isLeader) this.scheduleRebuild();
  }

  private onChannelMessage(message: ChannelMessage): void {
    switch (message.kind) {
      case "subscribe":
        if (this.isLeader) {
          this.tabTopics.set(message.tabId, new Set(message.topics));
          this.scheduleRebuild();
        }
        break;
      case "unsubscribe":
        if (this.isLeader) {
          this.tabTopics.delete(message.tabId);
          this.scheduleRebuild();
        }
        break;
      case "leader-ready":
        // A new leader took over; resend our topics so it can rebuild its union.
        this.publishOwnTopics();
        break;
      case "event":
        this.applyEvent(message.event, message.data);
        break;
      case "resync":
        this.invalidateActiveSnapshots();
        break;
    }
  }

  private becomeLeader(): () => void {
    this.isLeader = true;
    this.tabTopics.set(TAB_ID, new Set(this.ownTopicUnion()));
    this.channel?.postMessage({ kind: "leader-ready" } satisfies ChannelMessage);
    this.scheduleRebuild();
    return () => {
      this.isLeader = false;
      this.streamAbort?.abort();
      this.streamAbort = null;
    };
  }

  private scheduleRebuild(): void {
    if (this.rebuildTimer) clearTimeout(this.rebuildTimer);
    this.rebuildTimer = setTimeout(() => void this.rebuildStream(), REBUILD_DEBOUNCE_MS);
  }

  private unionTopics(): string[] {
    const set = new Set<string>();
    for (const topics of this.tabTopics.values()) {
      for (const topic of topics) set.add(topic);
    }
    return [...set];
  }

  private async rebuildStream(): Promise<void> {
    if (!this.isLeader) return;
    const topics = this.unionTopics();
    const key = topics.slice().sort().join(",");
    if (key === this.currentTopicsKey) return;
    this.currentTopicsKey = key;

    this.streamAbort?.abort();
    if (topics.length === 0) {
      this.streamAbort = null;
      return;
    }

    const controller = new AbortController();
    this.streamAbort = controller;
    try {
      const { stream } = await streamEvents({
        query: { topics: key },
        signal: controller.signal,
        onSseEvent: (event) => {
          this.channel?.postMessage({
            kind: "event",
            event: event.event ?? "message",
            data: event.data,
          } satisfies ChannelMessage);
          this.applyEvent(event.event ?? "message", event.data);
        },
        onSseError: () => {
          this.channel?.postMessage({ kind: "resync" } satisfies ChannelMessage);
          this.invalidateActiveSnapshots();
        },
      });
      // The generator drives onSseEvent as a side effect; draining it keeps
      // the connection (and its retry loop) alive for the stream's lifetime.
      const iterator = stream[Symbol.asyncIterator]();
      while (!controller.signal.aborted) {
        const { done } = await iterator.next();
        if (done) break;
      }
    } catch {
      // Aborted by a topic rebuild or leader handoff; nothing to report.
    }
  }

  private applyEvent(eventName: string, data: unknown): void {
    if (eventName === "ready") {
      this.ready = true;
      for (const listener of this.readyListeners) listener();
      return;
    }
    if (eventName === "resync") {
      this.invalidateActiveSnapshots();
      return;
    }
    if (eventName !== "step" || data == null) return;
    this.patcher.enqueue(data as StepEvent);
  }

  private invalidateActiveSnapshots(): void {
    this.ready = false;
    this.patcher.reset();
    const isSnapshotQuery = (query: { queryKey: readonly unknown[] }) => {
      const first = query.queryKey[0];
      const id = typeof first === "object" && first !== null ? (first as { _id?: string })._id : undefined;
      return id === "listJobs" || id === "listRunSteps";
    };
    void this.queryClient.invalidateQueries({ predicate: isSnapshotQuery, refetchType: "none" });
    const unsubscribe = this.onReady(() => {
      unsubscribe();
      void this.queryClient.refetchQueries({ predicate: isSnapshotQuery, type: "active" });
    });
  }
}

let singleton: SseBridge | null = null;

export function getSseBridge(queryClient: QueryClient): SseBridge {
  singleton ??= new SseBridge(queryClient);
  return singleton;
}
