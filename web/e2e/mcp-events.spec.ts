import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { BuilderPage } from "./builder";

const source = readFileSync(new URL("../../examples/mcp-events.yaml", import.meta.url), "utf8")
  .replace("${EVENT_DATABASE_URL}", "postgres://events.example.test/hookway");
for (const width of [1440, 390]) {
  test(`MCP event subscriptions export and validate at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(source);
    const events = await builder.openConfigItem("Events");
    await expect(events.getByRole("switch", { name: "Enable durable MCP delivery" })).toBeChecked();
    await events.getByLabel("Deployment namespace", { exact: true }).fill("agent-events-test");
    await page.screenshot({ path: testInfo.outputPath(`event-delivery-${width}.png`) });
    await builder.closeDialog();
    await builder.openInspector();
    await builder.inspector().getByRole("button", { name: /Select.*mcp/i }).click();
    await builder.inspector().getByRole("tab", { name: "Events (1)", exact: true }).click();
    await expect(builder.inspector().getByLabel("Named event", { exact: true })).toHaveValue("issue.opened");
    await expect(builder.inspector().getByLabel("Subscription argument schema")).toHaveValue(/repository/);
    await page.screenshot({ path: testInfo.outputPath(`event-subscription-${width}.png`) });
    await builder.closeInspector();
    const exported = await builder.openConfigItem("Export YAML");
    await expect(exported.getByTestId("validation-ok")).toBeVisible({ timeout: 30_000 });
    await expect(exported.getByTestId("export-core.yaml")).toContainText("agent-events-test");
    await expect(exported.getByTestId("export-core.yaml")).toContainText("mcp_events");
    await exported.getByLabel("YAML organization", { exact: true }).click();
    await page.getByRole("option", { name: "One YAML per flow", exact: true }).click();
    await expect(exported.getByTestId("validation-ok")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  });
}
