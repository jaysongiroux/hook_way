import { expect, test } from "@playwright/test";
import { BuilderPage } from "./builder";

const config = `lookups:
  customer:
    http: {url: 'https://lookup.example.test/customers/{key}', value_path: id}
destinations:
  workflows:
    type: temporal
    temporal: {host_port: 'localhost:7233', namespace: default}
flows:
  events:
    nodes:
      - {id: in, type: http_trigger, path: /events}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: test-secret, signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: customer, type: lookup, provider: customer, key: '{body.customer_id}', name: customer_id}
      - id: send
        type: publish
        destination: workflows
        target: orders
        temporal:
          workflow_type: OrderWorkflow
          input: body
          id_reuse_policy: reject_duplicate
          workflow_id: {source: body, path: id}
    edges: [in>body, body>auth, auth>env, env>customer, customer>send]
`;

for (const width of [1440, 390]) {
  test(`standalone lookup and Temporal edit, export and reload at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(config);
    await builder.selectNode("customer");
    await expect(
      builder
        .inspector()
        .getByRole("textbox", { name: "Lookup provider", exact: true }),
    ).toHaveValue("customer");
    await builder
      .inspector()
      .getByRole("button", { name: "Manage lookups", exact: true })
      .click();
    let dialog = page.getByRole("dialog", {
      name: "Lookup providers",
      exact: true,
    });
    await dialog
      .getByLabel("Request URL", { exact: true })
      .fill("https://lookup.example.test/v2/{key}");
    await dialog.getByLabel("Lookup timeout", { exact: true }).fill("3s");
    await page.screenshot({ path: testInfo.outputPath("lookup.png") });
    expect(
      await dialog.evaluate(
        (element) => element.scrollWidth - element.clientWidth,
      ),
    ).toBeLessThanOrEqual(1);
    await dialog
      .getByRole("button", { name: "Close Lookup providers" })
      .click();
    await expect(builder.inspector()).toBeVisible();
    await builder.selectNode("send");
    await builder
      .inspector()
      .getByLabel("Workflow type", { exact: true })
      .fill("InvoiceWorkflow");
    await builder
      .inspector()
      .getByLabel("Total execution timeout", { exact: true })
      .fill("24h");
    await page.screenshot({ path: testInfo.outputPath("temporal.png") });
    expect(
      await builder
        .inspector()
        .evaluate((element) => element.scrollWidth - element.clientWidth),
    ).toBeLessThanOrEqual(1);
    dialog = await builder.openConfigItem("Export YAML");
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeEnabled();
    await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
    await expect(dialog.locator("pre")).toContainText(
      "workflow_type: InvoiceWorkflow",
    );
    await expect(dialog.locator("pre")).toContainText(
      "https://lookup.example.test/v2/{key}",
    );
    await builder.closeDialog();
    await page.reload();
    await builder.selectNode("send");
    await expect(
      builder.inspector().getByLabel("Workflow type", { exact: true }),
    ).toHaveValue("InvoiceWorkflow");
    dialog = await builder.openConfigItem("Lookups");
    await expect(dialog.getByLabel("Request URL", { exact: true })).toHaveValue(
      "https://lookup.example.test/v2/{key}",
    );
    await expect(
      dialog.getByLabel("Lookup timeout", { exact: true }),
    ).toHaveValue("3s");
    expect(errors).toEqual([]);
  });
}
