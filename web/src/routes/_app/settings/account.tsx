import { createFileRoute } from "@tanstack/react-router";

import { AccountSettingsView } from "../../../features/auth/account-settings-view";

export const Route = createFileRoute("/_app/settings/account")({
  component: AccountSettingsView,
});
