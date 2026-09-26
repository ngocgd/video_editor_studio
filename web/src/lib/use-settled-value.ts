import { useEffect, useState } from "react";

/**
 * The value once it has stopped changing for `delayMs`. Until then the
 * previous settled value is returned, so a fetch keyed on it does not
 * fire for every intermediate value (holding an arrow key through a grid).
 */
export function useSettledValue<T>(value: T, delayMs: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return settled;
}
