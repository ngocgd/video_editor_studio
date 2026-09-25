import { createFileRoute } from "@tanstack/react-router";

import { LlmSettingsView } from "../../../features/settings-llm/llm-settings-view";

export const Route = createFileRoute("/_app/settings/llm")({
  component: LlmSettingsView,
});
