import type { LlmActionOverrides } from "../../api/gen/types.gen";
import { ClaudeCliStatusCard } from "./claude-cli-status-card";
import { LlmProviderCard } from "./llm-provider-card";
import { useLlmSettings, useUpdateLlmSettings } from "./use-llm-settings";

const ACTIONS: (keyof LlmActionOverrides)[] = ["outline", "draft", "rewrite", "translate", "scene_split", "summary"];

/** Settings > LLM providers (phase 6, AC8 in the UI): default provider, per-action override, key entry, Test, claude CLI status. */
export function LlmSettingsView() {
  const { data: settings } = useLlmSettings();
  const update = useUpdateLlmSettings();

  if (!settings) return <p className="text-sm text-text-2">Loading…</p>;

  const setDefault = (name: string) => {
    update.mutate({ body: { default: name, overrides: settings.overrides } });
  };

  const setOverride = (action: keyof LlmActionOverrides, provider: string) => {
    update.mutate({
      body: { default: settings.default, overrides: { ...settings.overrides, [action]: provider || undefined } },
    });
  };

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <h1 className="text-lg font-semibold">LLM providers</h1>

      <section className="flex flex-col gap-2">
        <h2 className="text-md font-medium">Provider</h2>
        <p className="text-xs text-text-2">The selected card is the default provider; it takes effect on the next AI action.</p>
        <div role="radiogroup" aria-label="Default LLM provider" className="grid grid-cols-2 gap-2">
          {settings.providers.map((provider) => (
            <LlmProviderCard key={provider.name} provider={provider} isDefault={provider.name === settings.default} onSetDefault={() => setDefault(provider.name)} />
          ))}
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <h2 className="text-md font-medium">Per-action overrides</h2>
        <div className="grid grid-cols-2 gap-3">
          {ACTIONS.map((action) => (
            <label key={action} className="flex flex-col gap-1 text-sm">
              {action.replace("_", " ")}
              <select
                className="h-8 rounded-md border border-input bg-well px-2 text-sm"
                value={settings.overrides[action] ?? ""}
                onChange={(e) => setOverride(action, e.target.value)}
              >
                <option value="">Use default ({settings.default})</option>
                {settings.providers.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      </section>

      <ClaudeCliStatusCard />
    </div>
  );
}
