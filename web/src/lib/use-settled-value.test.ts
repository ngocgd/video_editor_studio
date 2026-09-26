import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useSettledValue } from "./use-settled-value";

describe("useSettledValue", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("returns a value only after it stopped changing for the delay", () => {
    const { result, rerender } = renderHook(({ v }) => useSettledValue(v, 250), { initialProps: { v: "a" } });
    expect(result.current).toBe("a");
    for (const v of ["b", "c", "d"]) {
      rerender({ v });
      act(() => vi.advanceTimersByTime(100));
    }
    expect(result.current).toBe("a");
    act(() => vi.advanceTimersByTime(250));
    expect(result.current).toBe("d");
  });
});
