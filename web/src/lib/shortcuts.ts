import { useEffect, useRef } from "react";

/**
 * Global shortcut registry (guidelines §8). Each screen registers its own
 * scope with `useShortcut`; only the active (topmost) scope's handlers run,
 * so a Storyboard shortcut never fires while a dialog scope is open.
 */

export type ShortcutHandler = (event: KeyboardEvent) => void;

interface RegisteredShortcut {
  scope: string;
  keys: string;
  handlerRef: { current: ShortcutHandler };
}

const registry: RegisteredShortcut[] = [];
const activeScopes: string[] = ["global"];

/** Pushes a scope to the top of the active stack (e.g. opening a dialog). */
export function pushShortcutScope(scope: string): void {
  activeScopes.push(scope);
}

/** Pops the given scope back off the active stack. */
export function popShortcutScope(scope: string): void {
  const index = activeScopes.lastIndexOf(scope);
  if (index >= 0) {
    activeScopes.splice(index, 1);
  }
}

function normalizeKeys(event: KeyboardEvent): string {
  const parts: string[] = [];
  if (event.ctrlKey || event.metaKey) parts.push("ctrl");
  if (event.altKey) parts.push("alt");
  const key = event.key.toLowerCase();
  // A plain Shift + printable key (e.g. Shift+/ => "?") already reflects
  // the shift state in `event.key` itself; only add an explicit "shift"
  // segment when it is combined with another modifier or a non-printable
  // named key, otherwise "?" would never match a registered "?" shortcut
  // (review M7).
  const isPlainPrintable = key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
  if (event.shiftKey && !isPlainPrintable) parts.push("shift");
  if (!["control", "shift", "alt", "meta"].includes(key)) {
    parts.push(key);
  }
  return parts.join("+");
}

let listenerInstalled = false;
let sequenceKey: string | null = null;
let sequenceTimer: ReturnType<typeof setTimeout> | null = null;

function handleKeydown(event: KeyboardEvent): void {
  const target = event.target as HTMLElement | null;
  const isEditable =
    target != null &&
    (target.tagName === "INPUT" ||
      target.tagName === "TEXTAREA" ||
      target.isContentEditable);

  const combo = normalizeKeys(event);
  const topScope = activeScopes[activeScopes.length - 1];

  // G-then-letter sequence (guidelines §8): "g" primes a 600ms window for the
  // next letter, skipped while typing in a form field.
  if (!isEditable && combo === "g" && topScope === "global") {
    sequenceKey = "g";
    if (sequenceTimer) clearTimeout(sequenceTimer);
    sequenceTimer = setTimeout(() => {
      sequenceKey = null;
    }, 600);
    return;
  }

  const effectiveCombo = sequenceKey === "g" && !isEditable ? `g ${combo}` : combo;
  sequenceKey = null;

  if (isEditable && !combo.startsWith("ctrl") && effectiveCombo !== "escape") {
    return;
  }

  for (let i = registry.length - 1; i >= 0; i -= 1) {
    const entry = registry[i];
    if (entry.scope === topScope && entry.keys === effectiveCombo) {
      event.preventDefault();
      entry.handlerRef.current(event);
      return;
    }
  }
}

function ensureListener(): void {
  if (listenerInstalled) return;
  window.addEventListener("keydown", handleKeydown);
  listenerInstalled = true;
}

/**
 * Registers a keyboard shortcut for the component's lifetime. `keys` is a
 * normalized combo string, e.g. "ctrl+k", "?", "g d". `handler` is kept in
 * a ref and always called at its latest identity, so passing a fresh
 * inline arrow function on every render (the common case) does not
 * unregister and re-register the listener on every render (review M7).
 */
export function useShortcut(scope: string, keys: string, handler: ShortcutHandler): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    ensureListener();
    const entry: RegisteredShortcut = { scope, keys, handlerRef };
    registry.push(entry);
    return () => {
      const index = registry.indexOf(entry);
      if (index >= 0) registry.splice(index, 1);
    };
    // handlerRef is intentionally stable across renders; only scope/keys identity re-registers.
  }, [scope, keys]);
}
