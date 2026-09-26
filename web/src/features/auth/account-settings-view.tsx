import { Link } from "@tanstack/react-router";

import { Button } from "../../components/ui/button";
import { InspectorSection } from "../../components/shared/inspector-panel";
import { useLogout, useMe, useSwitchTenant } from "./use-auth";

export function AccountSettingsView() {
  const { data: me } = useMe();
  const switchTenant = useSwitchTenant();
  const logout = useLogout();

  if (!me) return null;

  return (
    <div className="flex max-w-lg flex-col gap-6">
      <InspectorSection title="Account">
        <p className="text-sm text-foreground">{me.email}</p>
      </InspectorSection>

      <InspectorSection title="Tenants">
        <ul className="flex flex-col gap-1.5">
          {me.tenants.map((tenant) => (
            <li key={tenant.tenantId} className="flex items-center justify-between text-sm">
              <span>
                {tenant.tenantName} <span className="text-text-2">({tenant.role})</span>
              </span>
              {tenant.tenantId === me.activeTenantId ? (
                <span className="text-xs text-primary-text">Active</span>
              ) : (
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={switchTenant.isPending}
                  onClick={() => switchTenant.mutate(tenant.tenantId)}
                >
                  Switch
                </Button>
              )}
            </li>
          ))}
        </ul>
      </InspectorSection>

      <InspectorSection title="Integrations">
        <Link to="/settings/youtube" className="text-sm text-primary-text hover:underline">
          YouTube channels
        </Link>
      </InspectorSection>

      <Button variant="destructive" size="sm" className="w-fit" onClick={() => logout.mutate()}>
        Log out
      </Button>
    </div>
  );
}
