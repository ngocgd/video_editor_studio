import type { DiffSegment } from "../../components/shared/diff-proposal";

/**
 * Word-level LCS diff between the original selected text and an AI action's
 * proposed replacement, producing plain-text `DiffSegment`s for the shared
 * `DiffProposal` (security checklist: "the diff is computed client-side on
 * plain strings, no HTML"). Splits on runs of whitespace, keeping the
 * whitespace itself as part of the following token so segments rejoin
 * losslessly.
 */
function tokenize(text: string): string[] {
  return text.match(/\S+\s*|\s+/g) ?? [];
}

export function diffText(original: string, proposed: string): DiffSegment[] {
  const a = tokenize(original);
  const b = tokenize(proposed);
  const n = a.length;
  const m = b.length;

  // Standard LCS table; selection-sized text keeps this well within budget.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i -= 1) {
    for (let j = m - 1; j >= 0; j -= 1) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const segments: DiffSegment[] = [];
  const push = (op: DiffSegment["op"], text: string) => {
    const last = segments[segments.length - 1];
    if (last && last.op === op) {
      last.text += text;
    } else {
      segments.push({ op, text });
    }
  };

  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      push("keep", a[i]);
      i += 1;
      j += 1;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      push("remove", a[i]);
      i += 1;
    } else {
      push("insert", b[j]);
      j += 1;
    }
  }
  while (i < n) {
    push("remove", a[i]);
    i += 1;
  }
  while (j < m) {
    push("insert", b[j]);
    j += 1;
  }

  return segments;
}
