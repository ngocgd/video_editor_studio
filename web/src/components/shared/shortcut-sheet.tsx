import { useState } from "react";

import { useShortcut } from "../../lib/shortcuts";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Kbd } from "./kbd";

const GLOBAL_SHORTCUTS: Array<{ keys: string[]; description: string }> = [
  { keys: ["Ctrl", "K"], description: "Open command palette" },
  { keys: ["Ctrl", "\\"], description: "Toggle nav rail" },
  { keys: ["Ctrl", "."], description: "Toggle inspector" },
  { keys: ["G", "D"], description: "Go to Dashboard" },
  { keys: ["G", "R"], description: "Go to Render Queue" },
  { keys: ["G", "S"], description: "Go to Settings" },
  { keys: ["?"], description: "Show this shortcut sheet" },
];

/** `?` opens the global shortcut reference sheet (guidelines §8). */
export function ShortcutSheet() {
  const [open, setOpen] = useState(false);
  useShortcut("global", "?", () => setOpen(true));

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-md">
        <DialogTitle className="text-lg font-semibold">Keyboard shortcuts</DialogTitle>
        <ul className="mt-3 flex flex-col gap-2">
          {GLOBAL_SHORTCUTS.map((shortcut) => (
            <li key={shortcut.description} className="flex items-center justify-between text-sm">
              <span className="text-text-2">{shortcut.description}</span>
              <span className="flex gap-1">
                {shortcut.keys.map((key) => (
                  <Kbd key={key}>{key}</Kbd>
                ))}
              </span>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
