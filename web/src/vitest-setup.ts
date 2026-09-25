import "@testing-library/jest-dom/vitest";
import { expect } from "vitest";
// vitest-axe's own "extend-expect" entry point resolves a different
// `expect` instance than the one vitest injects into test files (a known
// mismatch with vitest 5's chai-based expect), so the matcher is
// registered explicitly against this file's `expect` instead.
// vitest-axe 0.1.0's own type declarations do not match the vitest 5 that
// is actually installed (see ./vitest-axe-matchers.d.ts), so the runtime
// export is pulled in dynamically-typed here rather than fighting its
// upstream .d.ts.
import * as axeMatchers from "vitest-axe/matchers";

const toHaveNoViolations = (axeMatchers as unknown as { toHaveNoViolations: (results: unknown) => { pass: boolean; message: () => string } }).toHaveNoViolations;
expect.extend({ toHaveNoViolations });

// jsdom has no matchMedia, ResizeObserver or scrollIntoView; Radix and
// TanStack Virtual call these during mount/measure.
if (!window.matchMedia) {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}

if (!("ResizeObserver" in window)) {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  // @ts-expect-error -- test-only polyfill
  window.ResizeObserver = ResizeObserverStub;
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

// axe-core's colour-contrast rule probes canvas 2D context, which jsdom
// does not implement; a minimal stub is enough for the check to run.
const canvasStub = (() => ({
  fillRect: () => {},
  getImageData: () => ({ data: new Uint8ClampedArray(4) }),
  measureText: () => ({ width: 0 }),
})) as unknown as typeof HTMLCanvasElement.prototype.getContext;
HTMLCanvasElement.prototype.getContext = canvasStub;
