import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../openapi/openapi.gen.yaml",
  output: "src/api/gen",
  plugins: [
    "@hey-api/client-fetch",
    "@hey-api/schemas",
    "zod",
    "@tanstack/react-query",
  ],
});
