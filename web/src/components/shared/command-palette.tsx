import { useNavigate } from "@tanstack/react-router";
import { Command } from "cmdk";
import { LayoutDashboard, ListChecks, Settings } from "lucide-react";
import { useState } from "react";

import { useShortcut } from "../../lib/shortcuts";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";

interface CommandItem {
  id: string;
  label: string;
  icon: typeof LayoutDashboard;
  to: string;
}

const ITEMS: CommandItem[] = [
  { id: "dashboard", label: "Go to Dashboard", icon: LayoutDashboard, to: "/" },
  { id: "jobs", label: "Go to Render Queue", icon: ListChecks, to: "/jobs" },
  { id: "settings", label: "Go to Account settings", icon: Settings, to: "/settings/account" },
];

/** `Ctrl K` global command palette (guidelines §8). */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();

  useShortcut("global", "ctrl+k", () => setOpen(true));

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-md p-0" showClose={false}>
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <Command label="Command palette" className="overflow-hidden rounded-lg">
          <Command.Input
            autoFocus
            placeholder="Type a command or search..."
            className="w-full border-b border-border bg-transparent px-3 py-2.5 text-sm outline-none placeholder:text-muted-foreground"
          />
          <Command.List className="max-h-80 overflow-auto p-1">
            <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">
              No matching command.
            </Command.Empty>
            {ITEMS.map((item) => (
              <Command.Item
                key={item.id}
                value={item.label}
                onSelect={() => {
                  setOpen(false);
                  void navigate({ to: item.to });
                }}
                className="flex cursor-pointer items-center gap-2 rounded-sm px-2.5 py-2 text-sm data-[selected=true]:bg-accent"
              >
                <item.icon size={16} strokeWidth={1.75} aria-hidden="true" />
                {item.label}
              </Command.Item>
            ))}
          </Command.List>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
