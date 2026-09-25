/**
 * Installs the CSP `default` Trusted Types policy (see
 * deploy/caddy/Caddyfile: `require-trusted-types-for 'script'; trusted-types
 * default dompurify`). App code never renders HTML directly (ESLint
 * `react/no-danger` is an error and nothing here calls `innerHTML`); this
 * policy exists only so third-party code paths that must parse HTML (for
 * example ProseMirror clipboard parsing in a later phase) can sanitise
 * through DOMPurify with `RETURN_TRUSTED_TYPE` instead of throwing under the
 * CSP `require-trusted-types-for 'script'` directive.
 *
 * Nothing in this phase calls an HTML sink, so the policy only needs to
 * exist before the first one does, not before first paint; DOMPurify
 * (~15KB gzip) is dynamically imported here instead of sitting in every
 * route's eager bundle (review "bundle easy wins").
 */
export async function installTrustedTypesPolicy(): Promise<void> {
  const tt = window.trustedTypes;
  if (!tt) {
    return;
  }

  const { default: DOMPurify } = await import("dompurify");

  try {
    tt.createPolicy("default", {
      createHTML(input: string) {
        return DOMPurify.sanitize(input, { RETURN_TRUSTED_TYPE: true }) as unknown as string;
      },
      createScript() {
        throw new Error("script creation is not permitted by the default Trusted Types policy");
      },
      createScriptURL() {
        throw new Error("script URL creation is not permitted by the default Trusted Types policy");
      },
    });
  } catch {
    // A "default" policy already exists (e.g. React StrictMode double-invoke
    // in dev, or a second call from a test); the first registration wins.
  }
}
