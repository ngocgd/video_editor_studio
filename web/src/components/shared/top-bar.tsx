import { useRouterState } from "@tanstack/react-router";
import { ChevronDown, LogOut, User } from "lucide-react";

import { useLogout, useMe } from "../../features/auth/use-auth";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { CommandPaletteTrigger } from "./app-shell";

const PAGE_TITLES: Record<string, string> = {
  "/": "Dashboard",
  "/jobs": "Render Queue",
  "/settings/models": "Models & providers",
  "/settings/account": "Account settings",
  "/settings/youtube": "YouTube channels",
};

/** Top bar (guidelines §6): 44px, breadcrumb, `Ctrl K` search, user menu. */
export function TopBar() {
  const routerState = useRouterState();
  const { data: me } = useMe();
  const logout = useLogout();
  const title = PAGE_TITLES[routerState.location.pathname] ?? "Loomtale Studio";

  return (
    <header className="flex items-center justify-between border-b border-border px-3">
      <h1 className="text-lg font-semibold">{title}</h1>
      <div className="flex items-center gap-3">
        <CommandPaletteTrigger />
        {me && (
          <DropdownMenu>
            <DropdownMenuTrigger className="flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-text-2 hover:bg-accent hover:text-foreground">
              <User size={16} strokeWidth={1.75} aria-hidden="true" />
              {me.email}
              <ChevronDown size={14} strokeWidth={1.75} aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => logout.mutate()}>
                <LogOut size={14} strokeWidth={1.75} aria-hidden="true" />
                Log out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    </header>
  );
}
