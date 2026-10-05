import { test, expect, type Page } from "@playwright/test";
import { BuilderPage } from "./builder";
import { parse } from "yaml";

const fixture = `
state_stores:
  local: {namespace: browser-tests, memory: {}}
server: {addr: ':8080', max_body_bytes: 1048576}
destinations:
  queue: {type: sqs, sqs: {region: us-east-1}}
flows:
  incoming:
    nodes:
      - {id: in, type: http_trigger, path: /hooks/incoming, methods: [POST]}
      - {id: limit, type: rate_limit, store: local, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: verify, type: auth, scheme: hmac_sha256_hex, source: {kind: header, key: X-Signature}, secret_ref: {provider: env, name: WEBHOOK_SECRET}}
      - {id: decode, type: envelope, body_format: json}
      - {id: send, type: publish, destination: queue, target: incoming, create: true}
    edges: [in>limit, limit>body, body>verify, verify>decode, decode>send]
`;
async function choose(page: Page, label: string, name: string) {
  await page.getByRole("textbox", { name: label, exact: true }).click();
  await page.getByRole("option", { name, exact: true }).click();
}
for (const viewport of [
  { width: 1280, height: 800 },
  { width: 390, height: 844 },
]) {
  test(`strategy edits, repair navigation, persistence and YAML validation at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(fixture);
    await builder.selectNode("verify");
    await choose(page, "Authentication strategy", "JWT · HS256");
    const claim = page.getByLabel("Raw body SHA-256 claim", { exact: true });
    await expect(claim).toHaveAttribute("aria-invalid", "true");
    await builder.closeInspector();
    let dialog = await builder.openConfigItem("Export YAML");
    const errors = dialog.getByTestId("validation-errors");
    await expect(errors).toContainText("body_hash_claim is required");
    await expect(errors).not.toContainText("unreachable");
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeDisabled();
    await errors
      .getByRole("button", { name: "Open affected node" })
      .first()
      .click();
    await expect(builder.inspector()).toBeVisible();
    await claim.fill("body_sha256");
    await builder.closeInspector();
    dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-ok")).toBeVisible();
    let config = parse(
      await dialog.getByTestId("export-core.yaml").innerText(),
    );
    expect(
      config.flows.incoming.nodes.find(
        (n: { id: string }) => n.id === "verify",
      ),
    ).toMatchObject({ scheme: "jwt_hs256", body_hash_claim: "body_sha256" });
    await builder.closeDialog();
    await builder.selectNode("verify");
    await choose(page, "Authentication strategy", "HMAC SHA-256 · hexadecimal");
    await builder.closeInspector();
    dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-ok")).toBeVisible();
    config = parse(await dialog.getByTestId("export-core.yaml").innerText());
    expect(
      config.flows.incoming.nodes.find(
        (n: { id: string }) => n.id === "verify",
      ),
    ).not.toHaveProperty("body_hash_claim");
    await builder.closeDialog();
    await builder.selectNode("send");
    await page.getByRole("button", { name: /^Message attributes/ }).click();
    await page
      .getByRole("switch", { name: "Use Message attributes" })
      .press("Space");
    const json = page.getByLabel("Message attributes", { exact: true });
    await json.fill('{"broken":');
    await builder.closeInspector();
    await builder.openInspector();
    await page.getByRole("button", { name: /^Message attributes/ }).click();
    await expect(json).toHaveValue('{"broken":');
    await builder.closeInspector();
    dialog = await builder.openConfigItem("Export YAML");
    await expect(
      dialog.getByText("Unresolved form drafts", { exact: true }),
    ).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "Copy", exact: true }),
    ).toBeDisabled();
    await builder.closeDialog();
    await page.reload();
    await builder.openInspector();
    await builder.selectNode("send");
    await page.getByRole("button", { name: /^Message attributes/ }).click();
    await expect(json).toHaveValue('{"broken":');
    await page
      .getByRole("button", { name: "Revert Message attributes draft" })
      .click();
    await builder.closeInspector();
    dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-ok")).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeEnabled();
  });
}
