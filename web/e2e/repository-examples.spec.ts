import { expect, test, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { unzipSync, strFromU8 } from "fflate";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

async function openExample(page: Page, id: string) {
  const toggle = page.getByRole("button", { name: "Nodes", exact: true });
  if ((await toggle.getAttribute("aria-pressed")) !== "true") await toggle.click();
  await page.getByRole("tab", { name: "Examples", exact: true }).click();
  await page.getByRole("textbox", { name: "Search examples" }).fill(id);
  await page.getByRole("button", { name: `Import example ${id}`, exact: true }).click();
  return page.getByRole("dialog", { name: /^Import / });
}

async function savedFlow(page: Page) {
  // Observe the persisted result before reload; avoid an arbitrary debounce wait.
  return page.evaluate(async () => {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open("hookway-config-ui");
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    const saved = await new Promise<{ activeFlow?: string }>((resolve, reject) => {
      const request = db.transaction("graphs").objectStore("graphs").get("current.v4");
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    db.close();
    return saved?.activeFlow;
  });
}

test("every discovered example passes the real browser core validator", async ({ page }) => {
  await new BuilderPage(page).gotoReady();
  const result = await page.evaluate(async () => {
    const catalogPath = "/src/graph/repositoryExamples.ts";
    const validatorPath = "/src/graph/validate.ts";
    const { repositoryExamples } = await import(/* @vite-ignore */ catalogPath);
    const { validateBundle } = await import(/* @vite-ignore */ validatorPath);
    // Match the validation fixtures used by make validate-examples. Only the
    // validation copy is expanded: catalogue imports retain the original YAML.
    const environment: Record<string, string> = {
      WEBHOOK_URL: "https://example.com/hooks",
      SLACK_WEBHOOK_URL: "https://hooks.slack.test/services/T000/B000/dummy",
      DISCORD_WEBHOOK_URL: "https://discord.test/api/webhooks/000/dummy",
      XML_UPSTREAM_URL: "https://upstream.test/xml",
      DEMO_MCP_URL: "https://mcp.example.test/agents/mcp",
      DEMO_ISSUER: "https://identity.example.test/demo",
    };
    const failures = [];
    for (const example of repositoryExamples) {
      const original = JSON.stringify(example.files);
      const files = Object.fromEntries(Object.entries(example.files as Record<string, string>).map(([name, text]) => [
        name, text.replace(/\$\{([A-Z_]+)\}/g, (reference, variable) => environment[variable] ?? reference),
      ]));
      const issues = await validateBundle({ entry: example.entry, files });
      const errors = issues?.filter((issue: { severity: string }) => issue.severity !== "warning");
      if (!issues || errors.length) failures.push({ id: example.id, errors });
      if (JSON.stringify(example.files) !== original) throw new Error("Validation modified the catalogue");
    }
    return { count: repositoryExamples.length, failures };
  });
  expect(result.count).toBeGreaterThan(60);
  expect(result.failures).toEqual([]);
});

test("nodes-only chooses one flow and preserves the active core settings", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml("server: {addr: ':9999'}\nflows: {existing: {nodes: [{id: keep, type: http_trigger, path: /keep}], edges: []}}\n");
  const dialog = await openExample(page, "multi-yaml/core.yaml");
  await expect(dialog.getByRole("radio", { name: "Nodes only" })).toBeChecked();
  await dialog.getByLabel("Source flow").selectOption("billing");
  await dialog.getByRole("button", { name: "Import nodes", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: "Active flow" })).toHaveText("existing");
  const exported = await builder.openConfigItem("Export YAML");
  const config = parse(await exported.getByTestId("export-core.yaml").innerText());
  expect(config.server.addr).toBe(":9999");
  expect(Object.keys(config.flows)).toEqual(["existing"]);
  expect(config.flows.existing.nodes[0].id).toBe("keep");
  expect(config.flows.existing.nodes).toHaveLength(7);
  expect(config.state_stores).toBeUndefined();
});

test("full core replacement exports a split bundle, reimports, and persists after reload", async ({ page }, testInfo) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  let dialog = await openExample(page, "multi-yaml/core.yaml");
  await dialog.getByRole("radio", { name: "Nodes + core config" }).check();
  await expect(dialog.getByRole("note")).toContainText("All current core flows and settings will be replaced");
  await dialog.getByRole("button", { name: "Replace core configuration", exact: true }).click();
  await expect(dialog).toBeHidden();
  await builder.expectFlows(["orders", "billing"]);
  dialog = await builder.openConfigItem("Export YAML");
  const pending = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "Download hookway-config.zip" }).click();
  const file = testInfo.outputPath("example.zip");
  await (await pending).saveAs(file);
  const files = unzipSync(readFileSync(file));
  expect(Object.keys(files).sort()).toEqual(["core.yaml", "flows/billing.yaml", "flows/orders.yaml"]);
  expect(parse(strFromU8(files["core.yaml"])).flows.orders).toEqual({ file: "flows/orders.yaml" });
  dialog = await builder.openConfigItem("Import YAML");
  await dialog.getByRole("tab", { name: "Files / ZIP" }).click();
  await dialog.getByLabel("YAML files or ZIP", { exact: true }).setInputFiles(file);
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(() => savedFlow(page)).toBe("billing");
  await page.reload();
  await expect(page.getByRole("button", { name: "Active flow" })).toHaveText("billing");
  await builder.expectFlows(["orders", "billing"]);
  dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("export-core.yaml")).toContainText("flows/orders.yaml");
});

for (const width of [1440, 390]) {
  test(`examples panel and dialog fit at ${width}px and cancel safely`, async ({ page }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => localStorage.setItem("hookway.builder.paletteWidth", "220"));
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    const before = await page.getByRole("button", { name: "Active flow" }).innerText();
    let dialog = await openExample(page, "showcase/production.yaml");
    await expect(dialog.getByRole("button", { name: "Import nodes" })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`examples-dialog-${width}.png`) });
    expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
    const tabs = page.getByRole("tablist", { name: "Palette sections" });
    const tabSize = await tabs.evaluate((element) => ({ content: element.scrollWidth, width: element.getBoundingClientRect().width }));
    expect(tabSize.content).toBeLessThanOrEqual(Math.ceil(tabSize.width) + 1);
    await page.screenshot({ path: testInfo.outputPath(`examples-panel-${width}.png`) });
    await expect(page.getByRole("button", { name: "Active flow" })).toHaveText(before);
    // Successful import also closes the overlay on compact screens.
    dialog = await openExample(page, "showcase/production.yaml");
    await dialog.getByRole("button", { name: "Import nodes" }).click();
    await expect(dialog).toBeHidden();
    if (width === 390) await expect(tabs).toBeHidden();
    expect(errors).toEqual([]);
  });
}
