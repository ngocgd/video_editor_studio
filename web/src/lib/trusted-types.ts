import DOMPurify from "dompurify";

/**
 * Installs the CSP `default` Trusted Types policy (see
 * deploy/caddy/Caddyfile: `require-trusted-types-for 'script'; trusted-types
 * default dompurify`). App code never renders HTML directly (ESLint
 * `react/no-danger` is an error and nothing here calls `innerHTML`); this
 * policy exists only so third-party code paths that must parse HTML (for
 * example ProseMirror clipboard parsing in a later phase) can sanitise
 * through DOMPurify with `RETURN_TRUSTED_TYPE` instead of throwing under the
 * CSP `require-trusted-types-for 'script'` directive.
 */
export function installTrustedTypesPolicy(): void {
  const tt = window.trustedTypes;
  if (!tt) {
    return;
  }

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
