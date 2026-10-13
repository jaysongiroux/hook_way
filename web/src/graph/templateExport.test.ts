import { expect, test } from "vitest";
import { useGraphStore } from "./store";
import { templateSubgraph } from "./templates";
import { toConfig } from "./toConfig";
import { toYaml } from "./toYaml";
import { scanEnvVars } from "./envScan";

// The silent failure this whole feature has to avoid: a second insert of the
// same template exporting two nodes under one `id:`. store.test.ts checks the
// store's ids; this checks what actually reaches the YAML.
test("two inserts of one template export unique ids and a scannable env var", () => {
  useGraphStore.getState().clear();
  const sub = templateSubgraph("slack-notify")!;
  useGraphStore.getState().insertSubgraph(sub, { x: 100, y: 100 });
  useGraphStore.getState().insertSubgraph(sub, { x: 100, y: 400 });
  const yaml = toYaml(toConfig(useGraphStore.getState().document()).config);

  for (const id of ["fmt", "post", "fmt_2", "post_2"]) {
    expect(yaml, id).toContain(`id: ${id}`);
  }
  const ids = [...yaml.matchAll(/^\s*- id: (\S+)$/gm)].map((m) => m[1]);
  expect(new Set(ids).size, "duplicate exported id:").toBe(ids.length);
  expect(yaml).toContain("fmt_2>post_2");
  // EnvPanel derives its list by scanning the exported YAML, so the webhook
  // URL shows up in the environment panel with no extra wiring.
  expect(scanEnvVars(yaml)).toContain("SLACK_WEBHOOK_URL");
});

test("an inserted ingest template surfaces its rotating secret in the env scan", () => {
  useGraphStore.getState().clear();
  const sub = templateSubgraph("stripe-ingest")!;
  useGraphStore.getState().insertSubgraph(sub, { x: 0, y: 0 });
  const yaml = toYaml(toConfig(useGraphStore.getState().document()).config);
  expect(scanEnvVars(yaml)).toContain("STRIPE_WEBHOOK_SECRET");
  expect(yaml).toContain("secret_ref:");
  expect(yaml).not.toMatch(/secret:\s*["'][^$"']/);
});
