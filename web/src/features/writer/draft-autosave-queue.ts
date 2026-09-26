import { ApiError } from "../../api/client";
import type { EpisodeDraft, ParagraphOp } from "../../api/gen/types.gen";
import { diffParagraphs, type DiffParagraph } from "./paragraph-diff";

export type SaveStatus = "saved" | "unsaved" | "saving" | "retrying" | "conflict";

export interface DraftAutosaveOptions {
  /** Sends the ops with the version they were diffed against; throws ApiError on failure. */
  send: (expectedVersion: number, ops: ParagraphOp[]) => Promise<EpisodeDraft>;
  onSaved: (draft: EpisodeDraft) => void;
  onStatus: (status: SaveStatus) => void;
  debounceMs?: number;
  /** Backoff before retrying a save that failed for a reason other than a conflict. */
  retryDelaysMs?: number[];
}

const DEFAULT_RETRY_DELAYS_MS = [2_000, 5_000, 10_000, 30_000];

function toDiffParagraphs(draft: EpisodeDraft): DiffParagraph[] {
  return draft.paragraphs.map((p) => ({ id: p.id, text: p.text }));
}

/**
 * The autosave state of one draft in one language. It keeps the last
 * server state the editor is based on (`base`) and the editor's latest
 * paragraphs (`local`), and saves the diff between them:
 *
 * - at most one PATCH is in flight; edits made meanwhile are sent after it
 *   lands, diffed against the version it returned, so overlapping saves
 *   never reuse a stale `expectedVersion`;
 * - a 409 stops saving and reports `conflict`; any other failure (rate
 *   limit, server error, network) keeps the edits and retries with backoff;
 * - `flush` saves immediately, for Accept, a language switch and unmount.
 */
export class DraftAutosaveQueue {
  private base: { paragraphs: DiffParagraph[]; version: number };
  private local: DiffParagraph[];
  private inFlight = false;
  private debounce: ReturnType<typeof setTimeout> | null = null;
  private retry: ReturnType<typeof setTimeout> | null = null;
  private attempt = 0;
  private status: SaveStatus = "saved";
  private stopped = false;
  private pending: Promise<void> | null = null;

  constructor(
    seed: EpisodeDraft,
    private readonly opts: DraftAutosaveOptions,
  ) {
    this.base = { paragraphs: toDiffParagraphs(seed), version: seed.version };
    this.local = this.base.paragraphs;
  }

  get version(): number {
    return this.base.version;
  }

  /** True while the editor holds edits the server has not stored yet. */
  get dirty(): boolean {
    return diffParagraphs(this.base.paragraphs, this.local).length > 0;
  }

  /** Records the editor's current paragraphs and saves them after the debounce. */
  edit(current: DiffParagraph[]): void {
    this.local = current;
    if (this.status === "conflict") return;
    this.setStatus(this.inFlight ? "saving" : "unsaved");
    this.clearDebounce();
    this.debounce = setTimeout(() => this.flush(), this.opts.debounceMs ?? 1000);
  }

  /** Replaces the editor's paragraphs (an accepted AI proposal) and saves at once. */
  replace(next: DiffParagraph[]): void {
    this.local = next;
    this.flush();
  }

  /** The editor's latest paragraphs, including unsaved edits. */
  get current(): DiffParagraph[] {
    return this.local;
  }

  /**
   * Saves pending edits and resolves true once the server holds everything
   * the editor has, or false when a save is failing or conflicting (the
   * edits stay queued).
   */
  async settle(): Promise<boolean> {
    for (;;) {
      if (this.status === "conflict" || this.status === "retrying") return false;
      this.flush();
      if (!this.inFlight) return !this.dirty;
      await this.pending;
    }
  }

  /** Saves pending edits now instead of waiting for the debounce. */
  flush(): void {
    this.clearDebounce();
    if (this.inFlight || this.status === "conflict") return;
    if (this.retry) {
      clearTimeout(this.retry);
      this.retry = null;
    }
    const ops = diffParagraphs(this.base.paragraphs, this.local);
    if (ops.length === 0) {
      this.setStatus("saved");
      return;
    }
    this.inFlight = true;
    this.setStatus("saving");
    this.pending = this.opts.send(this.base.version, ops).then(
      (draft) => {
        this.inFlight = false;
        this.attempt = 0;
        this.base = { paragraphs: toDiffParagraphs(draft), version: draft.version };
        this.opts.onSaved(draft);
        // Edits typed while this save was in flight go out next.
        if (this.dirty) this.flush();
        else this.setStatus("saved");
      },
      (error: unknown) => {
        this.inFlight = false;
        if (error instanceof ApiError && error.status === 409) {
          this.setStatus("conflict");
          return;
        }
        this.scheduleRetry();
      },
    );
  }

  /** Drops local edits and continues from `latest` (Reload after a conflict). */
  reset(latest: EpisodeDraft): void {
    this.clearDebounce();
    this.base = { paragraphs: toDiffParagraphs(latest), version: latest.version };
    this.local = this.base.paragraphs;
    this.attempt = 0;
    this.setStatus("saved");
  }

  /**
   * Re-applies this editor's own changes (the diff from its base) on top of
   * `latest`, so a conflict can be resolved without dropping either side's
   * edits to other paragraphs. Resolves to the merged draft for the editor
   * to reload, or null when the changes no longer apply (still a conflict).
   */
  async mergeOnto(latest: EpisodeDraft): Promise<EpisodeDraft | null> {
    this.clearDebounce();
    const ops = diffParagraphs(this.base.paragraphs, this.local);
    if (ops.length === 0) {
      this.reset(latest);
      return latest;
    }
    this.inFlight = true;
    this.setStatus("saving");
    try {
      const merged = await this.opts.send(latest.version, ops);
      this.base = { paragraphs: toDiffParagraphs(merged), version: merged.version };
      this.local = this.base.paragraphs;
      this.attempt = 0;
      this.opts.onSaved(merged);
      this.setStatus("saved");
      return merged;
    } catch {
      this.setStatus("conflict");
      return null;
    } finally {
      this.inFlight = false;
    }
  }

  /** Stops timers; a save already in flight still completes. */
  stop(): void {
    this.stopped = true;
    this.clearDebounce();
    if (this.retry) clearTimeout(this.retry);
    this.retry = null;
  }

  private scheduleRetry(): void {
    this.setStatus("retrying");
    if (this.stopped) return;
    const delays = this.opts.retryDelaysMs ?? DEFAULT_RETRY_DELAYS_MS;
    const delay = delays[Math.min(this.attempt, delays.length - 1)];
    this.attempt += 1;
    this.retry = setTimeout(() => {
      this.retry = null;
      this.flush();
    }, delay);
  }

  private clearDebounce(): void {
    if (this.debounce) clearTimeout(this.debounce);
    this.debounce = null;
  }

  private setStatus(status: SaveStatus): void {
    if (status === this.status) return;
    this.status = status;
    this.opts.onStatus(status);
  }
}
