import { useState } from "react";

import type { ProviderStatus } from "../../api/gen/types.gen";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { usePutLlmApiKey, useTestLlmProvider } from "./use-llm-settings";

/** One provider card (phase 6 settings): availability, write-only key entry, Test button with latency. */
export function LlmProviderCard({ provider, isDefault, onSetDefault }: { provider: ProviderStatus; isDefault: boolean; onSetDefault: () => void }) {
  const putKey = usePutLlmApiKey();
  const test = useTestLlmProvider();
  const [apiKey, setApiKey] = useState("");

  const saveKey = () => {
    if (!apiKey.trim()) return;
    putKey.mutate({ path: { provider: provider.name }, body: { apiKey: apiKey.trim() } }, { onSuccess: () => setApiKey("") });
  };

  return (
    <div
      role="radio"
      aria-checked={isDefault}
      tabIndex={0}
      onClick={onSetDefault}
      onKeyDown={(e) => e.key === "Enter" && onSetDefault()}
      className={`flex cursor-pointer flex-col gap-2 rounded-md border p-3 ${isDefault ? "border-primary shadow-[inset_0_0_0_1px_var(--primary)]" : "border-border"}`}
    >
      <div className="flex items-center justify-between">
        <b className="text-sm font-semibold">{provider.name}</b>
        <span className={`rounded-sm px-1.5 py-0.5 text-xs ${provider.available ? "bg-success-muted text-success" : "bg-muted text-muted-foreground"}`}>
          {provider.available ? "Available" : "Unavailable"}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        {provider.configured ? "API key configured" : "No API key configured"}
        {provider.disabledReason ? ` · ${provider.disabledReason}` : ""}
      </p>

      <div className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
        <Input
          type="password"
          placeholder="API key"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          maxLength={4096}
        />
        <Button variant="secondary" size="sm" onClick={saveKey} disabled={putKey.isPending || !apiKey.trim()}>
          Save
        </Button>
      </div>

      <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => test.mutate({ body: { provider: provider.name } })}
          disabled={test.isPending}
        >
          {test.isPending ? "Testing…" : "Test"}
        </Button>
        {test.data && (
          <span className={`text-xs ${test.data.ok ? "text-success" : "text-destructive"}`}>
            {test.data.ok ? "OK" : "Failed"}
            {test.data.latencyMs != null ? ` · ${test.data.latencyMs}ms` : ""}
            {test.data.detail ? ` · ${test.data.detail}` : ""}
          </span>
        )}
      </div>
    </div>
  );
}
