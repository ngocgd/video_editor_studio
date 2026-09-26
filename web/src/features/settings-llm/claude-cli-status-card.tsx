import { useClaudeCliStatus } from "./use-llm-settings";

/** claude CLI status block (phase 6): version/auth state/"Tools disabled" from GET /settings/llm/cli-status. */
export function ClaudeCliStatusCard() {
  const { data: status, isLoading } = useClaudeCliStatus();

  if (isLoading) return <p className="text-sm text-text-2">Loading claude CLI status…</p>;
  if (!status) return null;

  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-card p-3 text-sm">
      <b className="font-semibold">claude CLI</b>
      <div className="flex flex-wrap gap-3 text-xs text-text-2">
        <span>{status.installed ? `Installed · v${status.version ?? "?"}` : "Not installed"}</span>
        <span className={status.authenticated ? "text-success" : "text-warning"}>
          {status.authenticated ? "Authenticated" : "Not authenticated"}
        </span>
        <span>{status.toolsDisabled ? "Tools disabled" : "Tools enabled"}</span>
      </div>
      {status.detail && <p className="text-xs text-muted-foreground">{status.detail}</p>}
    </div>
  );
}
