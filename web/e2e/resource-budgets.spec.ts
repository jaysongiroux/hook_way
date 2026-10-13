import { expect, test, type Locator } from "@playwright/test";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const fixture = `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations:
  sink:
    type: webhook
    webhook: {url: "http://localhost:9000", max_queue_bytes: 134217728}
flows:
  main:
    nodes:
      - {id: in, type: http_trigger, path: /events, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: fixture-only, signature_header: X-Fixture-Key}
      - {id: parse, type: envelope, body_format: json, json: {max_depth: 512, max_nodes: 10000}}
      - {id: out, type: publish, destination: sink, target: events}
    edges: [in>body, body>auth, auth>parse, parse>out]
`;

async function expectInViewport(control: Locator, width: number) {
  await control.scrollIntoViewIfNeeded();
  await expect(control).toBeVisible();
  const box = await control.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width);
}

for (const viewport of [
  { width: 1440, height: 900, name: "desktop" },
  { width: 390, height: 844, name: "mobile" },
]) {
  test(`resource budgets remain editable and round-trip on ${viewport.name}`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(fixture);
    await builder.selectNode("parse");
    const inspector = builder.inspector();
    await inspector.getByRole("button", { name: "Identity and description" }).click();
    const depth = inspector.getByLabel("Maximum JSON depth", { exact: true });
    const nodes = inspector.getByLabel("Maximum JSON values", { exact: true });
    await expect(depth).toHaveValue("512");
    await expect(nodes).toHaveValue("10000");
    await depth.fill("256");
    await nodes.fill("20000");
    await expectInViewport(depth, viewport.width);
    await expectInViewport(nodes, viewport.width);
    await expect(inspector).toContainText("original request bytes");
    await expect(inspector.getByText(/^Configuration checks pass\./)).toBeVisible();
    await expect(inspector).toHaveCSS("opacity", "1");
    await page.screenshot({ path: testInfo.outputPath(`json-budget-${viewport.name}.png`), animations: "disabled" });

    const destinations = await builder.openConfigItem("Destinations");
    await destinations.getByRole("button", { name: "Select sink", exact: true }).click();
    const queue = destinations.getByLabel("max queue bytes", { exact: true });
    await expect(queue).toHaveValue("134217728");
    await queue.fill("67108864");
    await expectInViewport(queue, viewport.width);
    await expect(destinations).toContainText("queued and active");
    await page.screenshot({ path: testInfo.outputPath(`queue-budget-${viewport.name}.png`), animations: "disabled" });
    await builder.closeDialog();

    const exported = await builder.openConfigItem("Export YAML");
    const config = parse(await exported.getByTestId("export-core.yaml").innerText());
    expect(config.flows.main.nodes.find((node: { id: string }) => node.id === "parse").json)
      .toEqual({ max_depth: 256, max_nodes: 20000 });
    expect(config.destinations.sink.webhook.max_queue_bytes).toBe(67108864);
    await builder.closeDialog();

    await page.reload();
    await builder.gotoReady();
    await builder.selectNode("parse");
    await expect(builder.inspector().getByLabel("Maximum JSON depth", { exact: true })).toHaveValue("256");
    await builder.inspector().getByRole("button", { name: "Use default json parser limits" }).click();
    await expect(builder.inspector().getByLabel("Maximum JSON depth", { exact: true })).toHaveValue("");
    await expect(builder.inspector().getByLabel("Maximum JSON depth", { exact: true })).toHaveAttribute("placeholder", "Default: 128");
    const resetExport = await builder.openConfigItem("Export YAML");
    const reset = parse(await resetExport.getByTestId("export-core.yaml").innerText());
    expect(reset.flows.main.nodes.find((node: { id: string }) => node.id === "parse")).not.toHaveProperty("json");
  });
}
