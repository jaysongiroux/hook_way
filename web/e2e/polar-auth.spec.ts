import { test, expect } from "@playwright/test";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const fixture = `
state_stores:
  local: {namespace: polar-browser-tests, memory: {}}
server: {addr: ':8080', max_body_bytes: 1048576}
destinations:
  queue: {type: sqs, sqs: {region: us-east-1}}
flows:
  incoming:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/polar, methods: [POST]}
      - {id: limit, type: rate_limit, store: local, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: verify, type: auth, scheme: hmac_sha256_hex, source: {kind: header, key: X-Signature}, secret_ref: {provider: env, name: POLAR_WEBHOOK_SECRET}}
      - {id: decode, type: envelope, body_format: json}
      - {id: send, type: publish, destination: queue, target: polar-events, create: true}
    edges: [in>limit, limit>body, body>verify, verify>decode, decode>send]
`;

for (const width of [1280, 390]) {
  test(`Polar authoring, round-trip and real Go validation at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(fixture);
    await builder.selectNode("verify");
    await page.getByRole("combobox", { name: "Authentication strategy", exact: true }).click();
    await page.getByRole("option", { name: "Polar · webhook signature", exact: true }).click();
    await expect(page.getByLabel("Signature header name", { exact: true })).toHaveValue("webhook-signature");
    await expect(page.getByLabel("timestamp header", { exact: true })).toHaveValue("webhook-timestamp");
    await expect(page.getByLabel("message id header", { exact: true })).toHaveValue("webhook-id");
    await expect(page.getByText(/Use the secret exactly as copied from Polar/)).toBeVisible();
    await builder.closeInspector();
    let dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-ok")).toBeVisible();
    const yaml = await dialog.getByTestId("export-core.yaml").innerText();
    const auth = parse(yaml).flows.incoming.nodes.find((node: { id: string }) => node.id === "verify");
    expect(auth).toMatchObject({ scheme: "hmac_sha256_polar", message_id_header: "webhook-id", timestamp_unit: "seconds", secret_ref: { provider: "env", name: "POLAR_WEBHOOK_SECRET" } });
    expect(auth).not.toHaveProperty("source");
    await builder.closeDialog();
    await builder.importYaml(yaml);
    await builder.selectNode("verify");
    await expect(page.getByLabel("message id header", { exact: true })).toHaveValue("webhook-id");
    await page.getByLabel("message id header", { exact: true }).fill("webhook-timestamp");
    await builder.closeInspector();
    dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-errors")).toContainText("distinct, nonempty signature, timestamp and message ID headers");
    await expect(dialog.getByRole("button", { name: "Download core.yaml" })).toBeDisabled();
  });
}
