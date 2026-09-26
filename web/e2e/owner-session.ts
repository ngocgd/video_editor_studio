import { fileURLToPath } from "node:url";

/** The seeded owner every spec runs as (override with LT_E2E_EMAIL / LT_E2E_PASSWORD). */
export const OWNER_EMAIL = process.env.LT_E2E_EMAIL ?? "owner@loomtale.local";
export const OWNER_PASSWORD = process.env.LT_E2E_PASSWORD ?? "LoomtaleDemo!2026";

/** Where global-setup.ts saves the signed-in owner's cookies (gitignored). */
export const OWNER_AUTH_FILE = fileURLToPath(new URL("../playwright/.auth/owner.json", import.meta.url));
