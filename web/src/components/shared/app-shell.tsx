import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { Cpu, FolderKanban, LayoutDashboard, ListChecks, PanelLeft, Search, Settings, Upload } from "lucide-react";
import { lazy, type ReactNode, Suspense, useState } from "react";

import { useShortcut } from "../../lib/shortcuts";
import { setCommandPaletteOpen } from "./command-palette-state";
import { StatusBar } from "./status-bar";
import { TopBar } from "./top-bar";

// The palette and shortcut sheet are opened on demand (Ctrl K / ?); loading
// cmdk and their dialogs lazily keeps them out of the initial route chunk
// (guidelines performance budget: initial JS <=200KB gzip).
const CommandPalette = lazy(() => import("./command-palette").then((m) => ({ default: m.CommandPalette })));
const ShortcutSheet = lazy(() => import("./shortcut-sheet").then((m) => ({ default: m.ShortcutSheet })));

const NAV_ITEMS = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/projects", label: "Projects", icon: FolderKanban },
  { to: "/import", label: "Import", icon: Upload },
  { to: "/jobs", label: "Render Queue", icon: ListChecks },
  { to: "/settings/models", label: "Models", icon: Cpu },
  { to: "/settings/account", label: "Settings", icon: Settings },
] as const;

/**
 * The app shell (guidelines §6): nav rail (56/224), 44px top bar, main
 * content, resizable inspector slot, 28px status bar. Every screen after
 * login composes this instead of building its own chrome.
 */
export function AppShell({ inspector, children }: { inspector?: ReactNode; children: ReactNode }) {
  const [railExpanded, setRailExpanded] = useState(true);
  const routerState = useRouterState();
  const navigate = useNavigate();

  useShortcut("global", "ctrl+\\", () => setRailExpanded((value) => !value));
  useShortcut("global", "g d", () => void navigate({ to: "/" }));
  useShortcut("global", "g r", () => void navigate({ to: "/jobs" }));
  useShortcut("global", "g s", () => void navigate({ to: "/settings/account" }));

  return (
    <div className="grid h-dvh grid-rows-[1fr_auto]">
      <div className="grid min-h-0" style={{ gridTemplateColumns: `${railExpanded ? 224 : 56}px 1fr` }}>
        <nav
          aria-label="Primary"
          className="flex flex-col gap-1 border-r border-border bg-background p-2 transition-[width] duration-[180ms] ease-out"
        >
          <div className="mb-1 flex h-8 items-center gap-2 px-2">
            <button
              type="button"
              onClick={() => setRailExpanded((value) => !value)}
              aria-label={railExpanded ? "Collapse navigation" : "Expand navigation"}
              className="rounded-md p-1 text-text-2 hover:bg-accent hover:text-foreground"
            >
              <PanelLeft size={16} strokeWidth={1.75} aria-hidden="true" />
            </button>
            {railExpanded && <img src="/logo.svg" alt="Loomtale Studio" width={140} height={23} />}
          </div>
          {NAV_ITEMS.map((item) => {
            const active = routerState.location.pathname === item.to;
            return (
              <Link
                key={item.to}
                to={item.to}
                title={railExpanded ? undefined : item.label}
                className={`flex h-8 items-center gap-2 rounded-md px-2 text-sm ${
                  active ? "bg-accent text-foreground" : "text-text-2 hover:bg-accent hover:text-foreground"
                }`}
                aria-current={active ? "page" : undefined}
              >
                <item.icon size={16} strokeWidth={1.75} aria-hidden="true" />
                <span className={railExpanded ? undefined : "sr-only"}>{item.label}</span>
              </Link>
            );
          })}
        </nav>
        <div className="grid min-h-0 grid-rows-[44px_1fr]">
          <TopBar />
          <div className="grid min-h-0" style={{ gridTemplateColumns: inspector ? "1fr 360px" : "1fr" }}>
            <main className="min-h-0 overflow-auto p-4">{children}</main>
            {inspector}
          </div>
        </div>
      </div>
      <StatusBar />
      <Suspense fallback={null}>
        <CommandPalette />
        <ShortcutSheet />
      </Suspense>
    </div>
  );
}

export function CommandPaletteTrigger() {
  return (
    <button
      type="button"
      onClick={() => setCommandPaletteOpen(true)}
      className="flex h-7 items-center gap-2 rounded-md border border-border bg-well px-2 text-xs text-text-2 hover:text-foreground"
    >
      <Search size={14} strokeWidth={1.75} aria-hidden="true" />
      Search
      <kbd className="hidden rounded-sm border border-border px-1 font-mono text-[10px] text-muted-foreground sm:inline">Ctrl K</kbd>
    </button>
  );
}
