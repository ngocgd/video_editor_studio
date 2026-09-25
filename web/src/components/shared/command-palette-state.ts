/**
 * Shared open/close state for the command palette, so the top-bar search
 * trigger and the `Ctrl K` shortcut both drive the same state directly
 * instead of the trigger faking a keyboard event through the shortcut
 * registry (review L3).
 */
let open = false;
const listeners = new Set<(open: boolean) => void>();

export function isCommandPaletteOpen(): boolean {
  return open;
}

export function setCommandPaletteOpen(next: boolean): void {
  open = next;
  for (const listener of listeners) listener(open);
}

export function onCommandPaletteOpenChange(listener: (open: boolean) => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
