import { createFileRoute } from "@tanstack/react-router";

import { VoicePresetsView } from "../../../features/presets/voice-presets-view";

export const Route = createFileRoute("/_app/settings/voices")({
  component: VoicePresetsView,
});
