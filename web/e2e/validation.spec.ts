import { test, expect } from "@playwright/test";
import { BuilderPage } from "./builder";
import { exampleYaml } from "./examples";

/** Verify the real Go bridge and its contract in a browser. A static dev server
 * cannot answer the editor API fallback, so this test also catches a missing
 * WASM artifact before validation assertions become misleading. */
test("the WASM validator is served and instantiated", async ({ page }) => {
  const response = await page.request.get("/hookway.wasm");
  // Deliberately not asserting response.ok(): the dev server answers unknown
  // paths with the SPA index.html at status 200, so a 2xx here is no evidence
  // the module exists. The content type is the real signal -- and it is part
  // of the contract anyway, because instantiateStreaming rejects a wrong MIME
  // type even when the bytes are correct.
  expect(
    response.headers()["content-type"],
    "hookway.wasm is not being served (got the SPA fallback instead) -- run `make wasm`",
  ).toContain("wasm");

  const builder = new BuilderPage(page);
  await builder.gotoReady();
  // The module is loaded lazily on first validation, which the export dialog
  // triggers.
  await builder.openConfigItem("Export YAML");
  await expect
    .poll(() => page.evaluate(() => typeof (window as unknown as { hookwayValidate?: unknown }).hookwayValidate), {
      timeout: 15_000,
    })
    .toBe("function");
  const bridge = await page.evaluate(() => {
    const wasm = window as unknown as {
      hookwayContractInfo: () => string;
      hookwayValidate: (yaml: string) => string;
      hookwayReplayValidate: (yaml: string) => string;
      hookwayReplayActionValidate: (json: string) => string;
    };
    return {
      info: JSON.parse(wasm.hookwayContractInfo()),
      core: JSON.parse(wasm.hookwayValidate("not: valid: yaml: [")),
      replay: JSON.parse(wasm.hookwayReplayValidate("not: valid: yaml: [")),
      action: JSON.parse(wasm.hookwayReplayActionValidate(JSON.stringify({ idempotency_key: "bad key" }))),
    };
  });
  expect(bridge.info).toMatchObject({ contract_version: 1 });
  expect(bridge.info.capabilities).toEqual(expect.arrayContaining([
    "core_yaml_validate", "replay_yaml_validate", "replay_action_input",
  ]));
  expect(bridge.core.issues.length).toBeGreaterThan(0);
  expect(bridge.replay.issues.length).toBeGreaterThan(0);
  expect(bridge.action.issues).toEqual(expect.arrayContaining([
    expect.objectContaining({ field: "idempotency_key" }),
  ]));
});

test("a config that only warns reports valid with warnings", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  // production.yaml validates with exactly one issue: a no_auth warning on the
  // trusted flow. Warnings must not block, so this asserts the severity split
  // rather than merely "no errors".
  await builder.importYaml(exampleYaml("showcase/production.yaml"));
  const dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-warnings")).toContainText(
    'flow "trusted" has a no_auth node',
    { timeout: 15_000 },
  );
  await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
});

test("a gate wired after read_body reports the mismatched port types", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(exampleYaml("showcase/invalid-gate-order.yaml"));
  const dialog = await builder.openConfigItem("Export YAML");
  const errors = dialog.getByTestId("validation-errors");
  await expect(errors).toBeVisible({ timeout: 15_000 });
  // The whole point of typed ports is that the operator sees which two types
  // disagree. This proves the Go compiler's message survives the WASM bridge
  // intact rather than being flattened into a generic "invalid config".
  await expect(errors).toContainText("read_body) produces RawRequest");
  await expect(errors).toContainText("rate_limit) consumes RequestHead");
  await expect(dialog.getByTestId("validation-ok")).toHaveCount(0);
});

// The multipart guard is a validate-time lint, not a compile error: the graph
// wires up fine and only fails once a request arrives. Its whole purpose is to
// tell the operator *before* that, so this asserts the message survives the
// WASM bridge and lands in the export dialog, not just that Lint() returns it.
const MULTIPART_FIELD_MAP = `server:
  addr: ":8080"
  max_body_bytes: 1048576
flows:
  uploads:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: multipart}
      - {id: mid, type: transform, kind: field_map, name: out, mappings: [{from: a, to: b}]}
      - {id: ack, type: respond, status: 202, body: ok}
    edges: [in>body, body>auth, auth>env, env>mid, mid>ack]
`;

test("a field_map transform behind a multipart envelope is reported as an error", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(MULTIPART_FIELD_MAP);
  const dialog = await builder.openConfigItem("Export YAML");
  const errors = dialog.getByTestId("validation-errors");
  await expect(errors).toBeVisible({ timeout: 15_000 });
  await expect(errors).toContainText("uses kind: field_map");
  await expect(errors).toContainText("body_format: multipart");
  // Only the message is asserted, not the issue's suggestion ("read the parts
  // in a code node instead"). validate.ts carries a `suggestion` field and the
  // CLI prints it, but nothing in the builder renders it -- for any issue, not
  // just this one. Asserting it here would fail for a reason unrelated to the
  // multipart guard.
  await expect(dialog.getByTestId("validation-ok")).toHaveCount(0);
});
