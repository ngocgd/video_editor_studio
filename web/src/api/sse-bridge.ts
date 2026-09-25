import type { QueryClient } from "@tanstack/react-query";

import { AiDeltaBus, isAiDeltaEvent } from "./sse-ai-delta";
import { SseCachePatcher, type StepEvent } from "./sse-cache";
import { SseConnection, type StreamStatus } from "./sse-connection";
import { broadcastChannelSupported, CHANNEL_NAME, electLeader } from "./sse-leader";
import { TopicRegistry } from "./sse-topic-registry";

export type { StreamStatus } from "./sse-connection";

type ChannelMessage =
  | { kind: "subscribe"; tabId: string; topics: string[] }
  | { kind: "unsubscribe"; tabId: string }
  | { kind: "leader-ready" }
  | { kind: "event"; event: string; data: unknown }
  | { kind: "resync" };

const REBUILD_DEBOUNCE_MS = 300;

/**
 * The single realtime path (guidelines "reuse points"): one leader tab owns
 * the actual SSE connection (see ./sse-connection) and fans events out over
 * a BroadcastChannel so every tab, including the leader, stays under the
 * server's per-user cap of 6 concurrent streams (RT#2). Every tab runs its
 * own bridge instance; only the leader's instance opens a network connection.
 */
export class SseBridge {
  // Generated per instance (not module scope) so a real page load and a
  // test process that constructs several bridges to simulate several tabs
  // both get one distinct id per bridge.
  private readonly tabId = crypto.randomUUID();
  private readonly channel: BroadcastChannel | null;
  private readonly patcher: SseCachePatcher;
  private readonly aiDeltaBus = new AiDeltaBus();
  private readonly connection: SseConnection;
  private readonly consumers = new Map<string, Set<string>>();
  private readonly topicRegistry = new TopicRegistry();
  private readonly readyListeners = new Set<() => void>();
  private readonly statusListeners = new Set<(status: StreamStatus) => void>();
  private rebuildTimer: ReturnType<typeof setTimeout> | null = null;
  private isLeader = false;
  private ready = false;
  private status: StreamStatus = "idle";
  private currentTopicsKey = "";

  constructor(queryClient: QueryClient) {
    this.patcher = new SseCachePatcher(queryClient);
    this.connection = new SseConnection({
      onEvent: (event) => {
        const name = event.event ?? "message";
        this.channel?.postMessage({ kind: "event", event: name, data: event.data } satisfies ChannelMessage);
        this.applyEvent(name, event.data);
      },
      onStatusChange: (status) => this.setStatus(status),
      onResync: () => {
        this.channel?.postMessage({ kind: "resync" } satisfies ChannelMessage);
        this.invalidateActiveSnapshots();
      },
    });
    this.channel = broadcastChannelSupported() ? new BroadcastChannel(CHANNEL_NAME) : null;
    this.channel?.addEventListener("message", (event: MessageEvent<ChannelMessage>) => {
      this.onChannelMessage(event.data);
    });
    if (typeof window !== "undefined") {
      // pagehide (not beforeunload, which hurts bfcache) reliably fires on
      // tab close/navigation and lets the leader drop this tab's topics
      // from the union instead of leaking them forever (review M3a).
      window.addEventListener("pagehide", () => {
        this.channel?.postMessage({ kind: "unsubscribe", tabId: this.tabId } satisfies ChannelMessage);
      });
    }
    electLeader(() => this.becomeLeader());
  }

  isReady(): boolean {
    return this.ready;
  }

  getStatus(): StreamStatus {
    return this.status;
  }

  onReady(listener: () => void): () => void {
    this.readyListeners.add(listener);
    return () => this.readyListeners.delete(listener);
  }

  onStatusChange(listener: (status: StreamStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  }

  /** A job-completion announcement (done/failed/canceled), for the status bar's live region. */
  onCompletion(listener: (evt: StepEvent) => void): () => void {
    return this.patcher.onCompletion(listener);
  }

  /** AI-action token chunks (phase 6 writer) for one step id; see ./sse-ai-delta for the contract gap this assumes. */
  onAiDelta(stepId: string, listener: (evt: { stepId: string; text: string; done: boolean }) => void): () => void {
    return this.aiDeltaBus.subscribe(stepId, listener);
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

  private setStatus(status: StreamStatus): void {
    if (this.status === status) return;
    this.status = status;
    for (const listener of this.statusListeners) listener(status);
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
    this.topicRegistry.set(this.tabId, topics);
    this.channel?.postMessage({ kind: "subscribe", tabId: this.tabId, topics } satisfies ChannelMessage);
    if (this.isLeader) this.scheduleRebuild();
  }

  private onChannelMessage(message: ChannelMessage): void {
    switch (message.kind) {
      case "subscribe":
        if (this.isLeader) {
          this.topicRegistry.set(message.tabId, message.topics);
          this.scheduleRebuild();
          // A tab joining (or re-announcing) while the topic union is
          // unchanged would otherwise never see a "ready" event and its
          // queries would stay gated forever (review M3b); replay it.
          if (this.ready) {
            this.channel?.postMessage({ kind: "event", event: "ready", data: null } satisfies ChannelMessage);
          }
        }
        break;
      case "unsubscribe":
        if (this.isLeader) {
          this.topicRegistry.delete(message.tabId);
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

  private becomeLeader(): void {
    this.isLeader = true;
    this.topicRegistry.set(this.tabId, this.ownTopicUnion());
    this.channel?.postMessage({ kind: "leader-ready" } satisfies ChannelMessage);
    this.scheduleRebuild();
  }

  private scheduleRebuild(): void {
    if (this.rebuildTimer) clearTimeout(this.rebuildTimer);
    this.rebuildTimer = setTimeout(() => this.applyDesiredTopics(), REBUILD_DEBOUNCE_MS);
  }

  private applyDesiredTopics(): void {
    if (!this.isLeader) return;
    const topics = this.topicRegistry.unionCapped();
    const key = topics.slice().sort().join(",");
    if (key === this.currentTopicsKey) return;
    this.currentTopicsKey = key;

    if (topics.length === 0) {
      this.connection.stop();
      this.setStatus("idle");
      return;
    }
    this.connection.start(key);
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
    if (eventName === "llm.delta") {
      if (isAiDeltaEvent(data)) this.aiDeltaBus.publish(data);
      return;
    }
    if (eventName !== "step" || data == null) return;
    this.patcher.enqueue(data as StepEvent);
  }

  private invalidateActiveSnapshots(): void {
    this.ready = false;
    this.patcher.invalidateSnapshots((listener) => this.onReady(listener));
  }
}

let singleton: SseBridge | null = null;

export function getSseBridge(queryClient: QueryClient): SseBridge {
  singleton ??= new SseBridge(queryClient);
  return singleton;
}
