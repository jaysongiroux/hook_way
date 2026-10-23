import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { BuilderPage } from "./builder";

const source = readFileSync(new URL("../../examples/adapters/named-events.yaml", import.meta.url), "utf8").replace("${WEBHOOK_URL}", "https://receiver.example.test");

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`named events can be edited, repaired and exported with real WASM at ${viewport.width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(source);
    let panel = await builder.openConfigItem("Events");
    await expect(panel.getByLabel("Name", { exact: true })).toHaveValue("issue.opened");
    await panel.getByLabel("New event name").fill("issue.opened");
    await panel.getByRole("button", { name: "Add event", exact: true }).click();
    await expect(panel.getByText("That name already exists.")).toBeVisible();
    await panel.getByLabel("New event name").fill("");
    await panel.getByLabel("Description", { exact: true }).fill("An issue was opened by a verified sender.");
    const schema = '{"type":"object","required":["issue_id","title"],"properties":{"issue_id":{"type":"string"},"title":{"type":"string"},"literal":{"const":"${KEPT_LITERAL}"}}}';
    await panel.getByLabel("Payload schema", { exact: true }).fill(schema);
    await page.screenshot({ path: testInfo.outputPath(`events-${viewport.width}.png`) });
    await builder.closeDialog();
    const exported = await builder.openConfigItem("Export YAML");
    await expect(exported.getByTestId("validation-ok")).toBeVisible({ timeout: 30_000 });
    await expect(exported.getByTestId("export-core.yaml")).toContainText("\\u0024{KEPT_LITERAL}");
    await builder.closeDialog();

    panel = await builder.openConfigItem("Events");
    await panel.getByLabel("Name", { exact: true }).fill("issue.created");
    await panel.getByRole("button", { name: "Rename", exact: true }).click();
    await builder.closeDialog();
    const broken = await builder.openConfigItem("Export YAML");
    await expect(broken.getByTestId("validation-errors")).toContainText("event is not defined");
    await broken.getByRole("button", { name: "Open affected node" }).click();
    await expect(builder.inspector().getByLabel("Event", { exact: true })).toHaveValue(/issue.opened/);
    await builder.inspector().getByLabel("Event", { exact: true }).click();
    await page.getByRole("option", { name: "issue.created", exact: true }).click();
    await builder.inspector().getByRole("button", { name: "Manage events" }).click();
    panel = page.getByRole("dialog", { name: "Events", exact: true });
    await expect(panel.getByLabel("Name", { exact: true })).toHaveValue("issue.created");
    await panel.getByLabel("Payload schema", { exact: true }).fill('{"type":"object","minimum":9007199254740993}');
    await expect(panel.getByText(/would lose precision/)).toBeVisible();
    await builder.closeDialog();
    // Catalog management returns to the inspector.
    await builder.closeInspector();
    await expect.poll(() => page.evaluate(() => new Promise<boolean>((resolve) => {
      const request = indexedDB.open("hookway-config-ui");
      request.onerror = () => resolve(false);
      request.onsuccess = () => {
        const db = request.result;
        const read = db.transaction("graphs", "readonly").objectStore("graphs").get("current.v4");
        read.onsuccess = () => {
          const drafts = read.result?.formDrafts ?? {};
          resolve(Object.values(drafts).some((draft) => (draft as { text?: string }).text === '{"type":"object","minimum":9007199254740993}'));
          db.close();
        };
        read.onerror = () => { db.close(); resolve(false); };
      };
    }))).toBe(true);
    await page.reload();
    await expect(page.getByRole("button", { name: "Config", exact: true })).toBeVisible();
    panel = await builder.openConfigItem("Events");
    await expect(panel.getByLabel("Payload schema", { exact: true })).toHaveValue('{"type":"object","minimum":9007199254740993}');
    await panel.getByRole("button", { name: "Revert payload schema draft" }).click();
    await builder.closeDialog();
    const repaired = await builder.openConfigItem("Export YAML");
    await expect(repaired.getByTestId("validation-ok")).toBeVisible({ timeout: 30_000 });
    await repaired.getByLabel("YAML organization", { exact: true }).click();
    await page.getByRole("option", { name: "One YAML per flow", exact: true }).click();
    await expect(repaired.getByTestId("validation-ok")).toBeVisible();
    const download = page.waitForEvent("download");
    await repaired.getByRole("button", { name: /Download hookway-config.zip/ }).click();
    expect((await download).suggestedFilename()).toBe("hookway-config.zip");
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    expect(overflow).toBe(false);
  });
}

test("invalid event schemas are diagnosed by real Go WASM", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(source.replace("{type: string, minLength: 1}", "{$ref: 'https://example.invalid/private-schema'}"));
  const panel = await builder.openConfigItem("Events");
  await expect(panel.getByText(/only local schema references are allowed/)).toBeVisible({ timeout: 30_000 });
});
