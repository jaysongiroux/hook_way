import { createExampleCatalog } from "./exampleCatalog";

// This module is requested only when the Examples tab opens. YAML stays local
// to the deployed frontend; no GitHub request or hand-maintained manifest.
const sources = import.meta.glob<string>([
  "../../../examples/**/*.{yaml,yml}",
  // Match the Docker context's private demo-output exclusions. These can
  // contain generated credentials and are not repository examples.
  "!../../../examples/mcp-events/demo/runtime/**",
  "!../../../examples/mcp-events/demo/evidence/**",
], {
  query: "?raw",
  import: "default",
  eager: true,
});

export const repositoryExamples = createExampleCatalog(Object.fromEntries(
  Object.entries(sources).map(([path, yaml]) => [path.replace("../../../examples/", ""), yaml]),
));
