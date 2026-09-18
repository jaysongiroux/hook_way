import { test, expect } from "@playwright/test";
import { BuilderPage } from "./builder";
import { exampleYaml } from "./examples";

/**
 * The builder validates configs client-side by running the real Go validator
 * compiled to WebAssembly. These tests exist because that wiring fails
 * *silently*: validate.ts swallows a load failure and falls back to a
 * /validate fetch the static dev server does not answer, leaving `issues`
 * null so neither the pass nor the fail banner renders. Asserting the asset
 * is served (below) turns a missing `make wasm` into an explicit failure
 * instead of a suite that passes without ever validating anything.
 */
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
});

test("a config that only warns still reports as passing", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  // production.yaml validates with exactly one issue: a no_auth warning on the
  // trusted flow. Warnings must not block, so this asserts the severity split
  // rather than merely "no errors".
  await builder.importYaml(exampleYaml("showcase/production.yaml"));
  const dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-ok")).toBeVisible({ timeout: 15_000 });
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
  await expect(errors).toContainText("body_format: multipart");
  await expect(errors).toContainText("read the parts in a code node instead");
  await expect(dialog.getByTestId("validation-ok")).toHaveCount(0);
});
