import { client } from "./gen/client.gen";

// The generated client defaults to the relative baseUrl "/api/v1". Browsers
// resolve a relative URL against the document location automatically, but
// Node's native Request/fetch (used by both Vitest+jsdom and any SSR/CLI
// context) has no document to resolve against and throws "Invalid URL" for
// a bare path. Resolving against window.location.origin here keeps the
// request relative to whatever origin actually served the page (so the
// Caddy-fronted web container still talks to its own /api/v1 path) while
// giving the Request constructor an absolute URL it can parse everywhere.
client.setConfig({ baseUrl: `${window.location.origin}/api/v1` });
