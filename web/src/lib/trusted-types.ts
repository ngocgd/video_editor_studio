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
 * Third-party components do write HTML sinks while rendering (Radix
 * ScrollArea's injected <style>), so main.tsx awaits this before the first
 * render. DOMPurify (~15KB gzip) is still dynamically imported here, in its
 * own chunk, instead of sitting in every route's eager bundle.
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
