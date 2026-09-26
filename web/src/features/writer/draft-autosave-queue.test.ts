import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../../api/client";
import type { EpisodeDraft, ParagraphOp } from "../../api/gen/types.gen";
import { DraftAutosaveQueue, type SaveStatus } from "./draft-autosave-queue";

function draft(version: number, paragraphs: [string, string][]): EpisodeDraft {
  return {
    episodeId: "e1",
    lang: "en",
    version,
    wordCount: 0,
    paragraphs: paragraphs.map(([id, text]) => ({ id, text, origin: "user", tainted: false })),
  } as EpisodeDraft;
}

/** A fake server holding one draft: applies upserts, rejects stale versions with 409. */
function fakeServer(initial: EpisodeDraft) {
  let current = initial;
  const calls: { expectedVersion: number; ops: ParagraphOp[] }[] = [];
  let failNext: ApiError | null = null;
  const pending: (() => void)[] = [];
  let hold = false;

  const send = vi.fn((expectedVersion: number, ops: ParagraphOp[]) => {
    calls.push({ expectedVersion, ops });
    return new Promise<EpisodeDraft>((resolve, reject) => {
      const settle = () => {
        if (failNext) {
          const error = failNext;
          failNext = null;
          reject(error);
          return;
        }
        if (expectedVersion !== current.version) {
          reject(new ApiError({ title: "version conflict", status: 409 }));
          return;
        }
        const paragraphs = [...current.paragraphs];
        for (const op of ops) {
          const i = paragraphs.findIndex((p) => p.id === op.paragraphId);
          if (op.op === "upsert" && i >= 0) paragraphs[i] = { ...paragraphs[i], text: op.text ?? "" };
          else if (op.op === "upsert") paragraphs.push({ id: op.paragraphId, text: op.text ?? "", origin: "user", tainted: false });
          else if (op.op === "delete" && i >= 0) paragraphs.splice(i, 1);
        }
        current = { ...current, version: current.version + 1, paragraphs };
        resolve(current);
      };
      if (hold) pending.push(settle);
      else settle();
    });
  });

  return {
    send,
    calls,
    get current() {
      return current;
    },
    /** Someone else saves a newer version. */
    externalWrite(next: EpisodeDraft) {
      current = next;
    },
    failNextWith(error: ApiError) {
      failNext = error;
    },
    holdResponses(on: boolean) {
      hold = on;
    },
    releaseOne() {
      pending.shift()?.();
    },
  };
}

function makeQueue(server: ReturnType<typeof fakeServer>, seed: EpisodeDraft) {
  const statuses: SaveStatus[] = [];
  const saved: EpisodeDraft[] = [];
  const queue = new DraftAutosaveQueue(seed, {
    send: server.send,
    onSaved: (d) => saved.push(d),
    onStatus: (s) => statuses.push(s),
    debounceMs: 1000,
    retryDelaysMs: [2000],
  });
  return { queue, statuses, saved };
}

describe("DraftAutosaveQueue", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("saves the diff against the last saved version after the debounce", async () => {
    const seed = draft(1, [["p1", "a"]]);
    const server = fakeServer(seed);
    const { queue, statuses } = makeQueue(server, seed);

    queue.edit([{ id: "p1", text: "ab" }]);
    queue.edit([{ id: "p1", text: "abc" }]);
    expect(server.send).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1000);

    expect(server.calls).toEqual([{ expectedVersion: 1, ops: [{ op: "upsert", paragraphId: "p1", text: "abc" }] }]);
    expect(queue.version).toBe(2);
    expect(statuses.at(-1)).toBe("saved");
  });

  it("sends edits made during an in-flight save afterwards, against the new version", async () => {
    const seed = draft(1, [["p1", "a"]]);
    const server = fakeServer(seed);
    const { queue } = makeQueue(server, seed);

    server.holdResponses(true);
    queue.replace([{ id: "p1", text: "first" }]);
    queue.edit([{ id: "p1", text: "second" }]);
    queue.flush(); // must not start a second overlapping PATCH
    expect(server.calls).toHaveLength(1);

    server.holdResponses(false);
    server.releaseOne();
    await vi.advanceTimersByTimeAsync(0);

    expect(server.calls.map((c) => c.expectedVersion)).toEqual([1, 2]);
    expect(server.current.paragraphs[0].text).toBe("second");
    expect(queue.dirty).toBe(false);
  });

  it("keeps edits and retries after a non-conflict failure", async () => {
    const seed = draft(1, [["p1", "a"]]);
    const server = fakeServer(seed);
    const { queue, statuses } = makeQueue(server, seed);

    server.failNextWith(new ApiError({ title: "Too Many Requests", status: 429 }));
    queue.replace([{ id: "p1", text: "kept" }]);
    await vi.advanceTimersByTimeAsync(0);
    expect(statuses.at(-1)).toBe("retrying");
    expect(queue.dirty).toBe(true);

    await vi.advanceTimersByTimeAsync(2000);
    expect(server.current.paragraphs[0].text).toBe("kept");
    expect(statuses.at(-1)).toBe("saved");
  });

  it("stops saving on a conflict instead of overwriting the other writer", async () => {
    const seed = draft(1, [["p1", "a"], ["p2", "b"]]);
    const server = fakeServer(seed);
    const { queue, statuses } = makeQueue(server, seed);

    server.externalWrite(draft(2, [["p1", "a"], ["p2", "theirs"]]));
    queue.replace([{ id: "p1", text: "mine" }, { id: "p2", text: "b" }]);
    await vi.advanceTimersByTimeAsync(0);
    expect(statuses.at(-1)).toBe("conflict");

    queue.edit([{ id: "p1", text: "mine!" }, { id: "p2", text: "b" }]);
    await vi.advanceTimersByTimeAsync(5000);
    expect(server.calls).toHaveLength(1);
    expect(server.current.paragraphs[1].text).toBe("theirs");
  });

  it("merges its own changes onto the latest version when keeping mine", async () => {
    const seed = draft(1, [["p1", "a"], ["p2", "b"]]);
    const server = fakeServer(seed);
    const { queue } = makeQueue(server, seed);

    server.externalWrite(draft(2, [["p1", "a"], ["p2", "theirs"]]));
    queue.replace([{ id: "p1", text: "mine" }, { id: "p2", text: "b" }]);
    await vi.advanceTimersByTimeAsync(0);

    const merged = await queue.mergeOnto(server.current);
    expect(merged?.paragraphs.map((p) => p.text)).toEqual(["mine", "theirs"]);
    expect(queue.current.map((p) => p.text)).toEqual(["mine", "theirs"]);
    expect(queue.dirty).toBe(false);
  });

  it("settles once pending edits are stored, and reports a failing save", async () => {
    const seed = draft(1, [["p1", "a"]]);
    const server = fakeServer(seed);
    const { queue } = makeQueue(server, seed);

    queue.edit([{ id: "p1", text: "typed" }]);
    await expect(queue.settle()).resolves.toBe(true);
    expect(server.current.paragraphs[0].text).toBe("typed");

    server.failNextWith(new ApiError({ title: "Service Unavailable", status: 503 }));
    queue.edit([{ id: "p1", text: "more" }]);
    await expect(queue.settle()).resolves.toBe(false);
    expect(queue.dirty).toBe(true);
  });

  it("drops local edits on reset", async () => {
    const seed = draft(1, [["p1", "a"]]);
    const server = fakeServer(seed);
    const { queue, statuses } = makeQueue(server, seed);

    queue.edit([{ id: "p1", text: "local" }]);
    queue.reset(draft(3, [["p1", "server"]]));
    await vi.advanceTimersByTimeAsync(5000);

    expect(server.send).not.toHaveBeenCalled();
    expect(queue.version).toBe(3);
    expect(statuses.at(-1)).toBe("saved");
  });
});
