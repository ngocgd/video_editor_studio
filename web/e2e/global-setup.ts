import { chromium, type FullConfig } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { OWNER_AUTH_FILE, OWNER_EMAIL, OWNER_PASSWORD } from "./owner-session";

/**
 * Signs the seeded owner in once per run and saves the session cookie for
 * every spec (see `use.storageState` in playwright.config.ts).
 *
 * The API limits logins per account (a burst of 5, then one every 12 s),
 * which is a security control and stays as it is. Signing in once here
 * keeps the whole suite at two logins (this one and smoke.spec.ts, which
 * exercises the login form itself) however many spec files there are.
 */
export default async function globalSetup(config: FullConfig): Promise<void> {
  const baseURL = config.projects[0]?.use.baseURL;
  if (!baseURL) throw new Error("playwright.config.ts must set use.baseURL");

  const browser = await chromium.launch();
  try {
    const page = await browser.newPage({ baseURL });
    await page.goto("/login");
    await page.getByLabel("Email").fill(OWNER_EMAIL);
    await page.getByLabel("Password").fill(OWNER_PASSWORD);
    await page.getByRole("button", { name: "Sign in" }).click();
    await page.waitForURL("**/");
    mkdirSync(dirname(OWNER_AUTH_FILE), { recursive: true });
    await page.context().storageState({ path: OWNER_AUTH_FILE });
  } finally {
    await browser.close();
  }
}
