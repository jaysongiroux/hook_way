import { expect, test } from "@playwright/test";
import { BuilderPage } from "./builder";

const config = `
state_stores:
  local: {namespace: browser-tests, memory: {}}
server: {addr: ':8080', max_body_bytes: 1048576}
destinations:
  sink:
    type: webhook
    webhook: {url: 'https://example.test/events'}
flows:
  events:
    nodes:
      - {id: in, type: http_trigger, path: /events}
      - {id: limit, type: rate_limit, store: local, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: test-secret, signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: send, type: publish, destination: sink, target: events}
    edges: [in>limit, limit>body, body>auth, auth>env, env>send]
`;

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
]) {
  test(`production settings persist and explain volatile delivery at ${viewport.width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize(viewport);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(config);
    await builder.selectNode("limit");
    const capacity = builder
      .inspector()
      .getByLabel("Maximum tracked identities", { exact: true });
    await capacity.fill("2000");
    await expect(capacity).toHaveValue("2000");
    await page.screenshot({ path: testInfo.outputPath("limiter.png") });

    let dialog = await builder.openConfigItem("Core server");
    await dialog.getByLabel("Traffic withdrawal delay").fill("5s");
    await dialog.getByLabel("Total shutdown limit").fill("90s");
    await builder.closeDialog();
    await builder.selectNode("send");
    await expect(
      builder.inspector().getByText("Queued in memory", { exact: true }),
    ).toBeVisible();
    const alert = builder.inspector().getByRole("alert");
    await expect(alert).toContainText(
      "even with replay or a dead-letter destination",
    );
    const overflow = await alert.evaluate(
      (element) => element.scrollWidth - element.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(1);
    await page.screenshot({ path: testInfo.outputPath("delivery.png") });

    dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-warnings")).toContainText(
      "async webhook delivery acknowledges an in-memory queue",
    );
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeEnabled();
    await expect(dialog.locator("pre")).toContainText("max_keys: 2000");
    await expect(dialog.locator("pre")).toContainText(
      "shutdown_total_timeout: 90s",
    );
    await builder.closeDialog();
    await page.reload();
    await builder.selectNode("limit");
    await expect(capacity).toHaveValue("2000");
    dialog = await builder.openConfigItem("Core server");
    await expect(dialog.getByLabel("Traffic withdrawal delay")).toHaveValue(
      "5s",
    );
    await expect(dialog.getByLabel("Total shutdown limit")).toHaveValue("90s");
    await dialog.getByLabel("Total shutdown limit").scrollIntoViewIfNeeded();
    await page.screenshot({
      path: testInfo.outputPath("shutdown.png"),
      animations: "disabled",
    });
    expect(errors).toEqual([]);
  });
}
