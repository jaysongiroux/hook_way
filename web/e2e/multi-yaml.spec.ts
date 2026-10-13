import { expect, test, type Locator } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { strToU8, unzipSync, strFromU8, zipSync } from "fflate";
import { parse, stringify } from "yaml";
import { BuilderPage } from "./builder";

const root = join(import.meta.dirname, "../..");
const entry = readFileSync(join(root, "examples/multi-yaml/core.yaml"), "utf8");
const orders = readFileSync(
  join(root, "examples/multi-yaml/flows/orders.yaml"),
  "utf8",
);
const billing = readFileSync(
  join(root, "examples/multi-yaml/flows/billing.yaml"),
  "utf8",
);
const inline = stringify({
  ...parse(entry),
  flows: { orders: parse(orders), billing: parse(billing) },
});
const replay = readFileSync(join(root, "examples/replay/replay.yaml"), "utf8");

async function choose(dialog: Locator, label: string, option: string) {
  await dialog.getByLabel(label, { exact: true }).click();
  await dialog
    .page()
    .getByRole("option", { name: option, exact: true })
    .click();
}

async function importFiles(builder: BuilderPage) {
  const dialog = await builder.openConfigItem("Import YAML");
  await dialog.getByRole("tab", { name: "Files / ZIP" }).click();
  return dialog;
}

test("split ZIP contains every flow and replay, reimports, and persists filenames", async ({
  page,
}, testInfo) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(inline);
  await builder.importReplayYaml(replay);
  let dialog = await builder.openConfigItem("Export YAML");
  await choose(dialog, "YAML organization", "One YAML per flow");
  await dialog
    .getByRole("button", { name: "flows/orders.yaml — orders", exact: true })
    .click();
  await dialog.getByLabel("Flow YAML filename").fill("hooks/orders.yml");
  await expect(dialog.getByTestId("export-hooks/orders.yml")).toContainText(
    "nodes:",
  );
  await expect(
    dialog.getByRole("button", { name: "Download hookway-config.zip" }),
  ).toBeEnabled();
  const pending = page.waitForEvent("download");
  await dialog
    .getByRole("button", { name: "Download hookway-config.zip" })
    .click();
  const download = await pending;
  expect(download.suggestedFilename()).toBe("hookway-config.zip");
  const filename = testInfo.outputPath("configuration.zip");
  await download.saveAs(filename);
  const files = unzipSync(readFileSync(filename));
  expect(Object.keys(files).sort()).toEqual([
    "core.yaml",
    "flows/billing.yaml",
    "hooks/orders.yml",
    "replay.yaml",
  ]);
  expect(parse(strFromU8(files["core.yaml"])).flows.orders).toEqual({
    file: "hooks/orders.yml",
  });
  expect(parse(strFromU8(files["hooks/orders.yml"])).nodes).toHaveLength(6);
  expect(parse(strFromU8(files["replay.yaml"])).database).toBeTruthy();
  dialog = await importFiles(builder);
  await dialog
    .getByLabel("YAML files or ZIP", { exact: true })
    .setInputFiles(filename);
  await expect(dialog.getByLabel("Entry YAML", { exact: true })).toHaveValue(
    "core.yaml",
  );
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(dialog).toBeHidden();
  await builder.expectFlows(["billing", "orders"]);
  // Wait for persistence by observing IndexedDB, avoiding an arbitrary delay.
  await expect
    .poll(async () =>
      page.evaluate(async () => {
        const db = await new Promise<IDBDatabase>((resolve, reject) => {
          const request = indexedDB.open("hookway-config-ui");
          request.onsuccess = () => resolve(request.result);
          request.onerror = () => reject(request.error);
        });
        const saved = await new Promise<{ flowFiles?: Record<string, string> }>(
          (resolve, reject) => {
            const request = db
              .transaction("graphs")
              .objectStore("graphs")
              .get("current.v4");
            request.onsuccess = () => resolve(request.result);
            request.onerror = () => reject(request.error);
          },
        );
        db.close();
        return saved?.flowFiles?.orders;
      }),
    )
    .toBe("hooks/orders.yml");
  await page.reload();
  dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByLabel("YAML organization")).toHaveValue(
    "One YAML per flow",
  );
  await dialog
    .getByRole("button", { name: "hooks/orders.yml — orders", exact: true })
    .click();
  await expect(dialog.getByLabel("Flow YAML filename")).toHaveValue(
    "hooks/orders.yml",
  );
  await page.screenshot({
    path: testInfo.outputPath("split-export-desktop.png"),
    fullPage: true,
  });
});

test("inline multi-flow downloads stay inline and one flow without replay downloads YAML", async ({
  page,
}, testInfo) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(inline);
  let dialog = await builder.openConfigItem("Export YAML");
  await expect(
    dialog.getByRole("button", { name: "Download hookway-config.zip" }),
  ).toBeEnabled();
  let pending = page.waitForEvent("download");
  await dialog
    .getByRole("button", { name: "Download hookway-config.zip" })
    .click();
  let download = await pending;
  const filename = testInfo.outputPath("inline.zip");
  await download.saveAs(filename);
  const files = unzipSync(readFileSync(filename));
  expect(Object.keys(files)).toEqual(["core.yaml"]);
  expect(parse(strFromU8(files["core.yaml"])).flows.orders.nodes).toHaveLength(
    6,
  );
  await builder.importYaml(
    stringify({ ...parse(entry), flows: { orders: parse(orders) } }),
  );
  dialog = await builder.openConfigItem("Export YAML");
  await expect(
    dialog.getByRole("button", { name: "Download core.yaml" }),
  ).toBeEnabled();
  pending = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "Download core.yaml" }).click();
  download = await pending;
  expect(download.suggestedFilename()).toBe("core.yaml");
});

test("flat file import attaches missing reference paths and rejects invalid imports atomically", async ({
  page,
}) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(inline);
  let dialog = await importFiles(builder);
  await dialog.getByLabel("YAML files or ZIP", { exact: true }).setInputFiles([
    {
      name: "core.yaml",
      mimeType: "application/yaml",
      buffer: Buffer.from(entry),
    },
    {
      name: "orders.yaml",
      mimeType: "application/yaml",
      buffer: Buffer.from(orders),
    },
    {
      name: "billing.yaml",
      mimeType: "application/yaml",
      buffer: Buffer.from(billing),
    },
  ]);
  await expect(
    dialog.getByRole("button", { name: "Import workspace" }),
  ).toBeDisabled();
  await choose(dialog, "Attach file to flows/orders.yaml", "orders.yaml");
  await choose(dialog, "Attach file to flows/billing.yaml", "billing.yaml");
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(dialog).toBeHidden();
  await builder.expectFlows(["orders", "billing"]);
  dialog = await importFiles(builder);
  const bad = zipSync({
    "core.yaml": strToU8(entry),
    "flows/orders.yaml": strToU8("nodes: ["),
    "flows/billing.yaml": strToU8(billing),
  });
  await dialog
    .getByLabel("YAML files or ZIP", { exact: true })
    .setInputFiles({
      name: "bad.zip",
      mimeType: "application/zip",
      buffer: Buffer.from(bad),
    });
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(
    dialog.getByText("Could not import files", { exact: true }),
  ).toBeVisible();
  await builder.closeDialog();
  await builder.expectFlows(["orders", "billing"]);
});

test("directory import preserves paths and file errors navigate to the affected preview", async ({
  page,
}) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  let dialog = await importFiles(builder);
  await dialog
    .getByLabel("Configuration directory", { exact: true })
    .setInputFiles(join(root, "examples/multi-yaml"));
  await expect(dialog.getByLabel("Entry YAML", { exact: true })).toHaveValue(
    "core.yaml",
  );
  await expect(
    dialog.getByRole("button", { name: "Import workspace" }),
  ).toBeEnabled();
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(dialog).toBeHidden();
  dialog = await importFiles(builder);
  const bad = zipSync({
    "core.yaml": strToU8(entry),
    "flows/orders.yaml": strToU8(orders.replace("status: 202", "status: 999")),
    "flows/billing.yaml": strToU8(billing),
  });
  await dialog
    .getByLabel("YAML files or ZIP", { exact: true })
    .setInputFiles({
      name: "invalid.zip",
      mimeType: "application/zip",
      buffer: Buffer.from(bad),
    });
  await dialog.getByRole("button", { name: "Import workspace" }).click();
  await expect(dialog).toBeHidden();
  dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-errors")).toContainText(
    "flows/orders.yaml",
  );
  await dialog
    .getByRole("button", { name: "Show flows/orders.yaml", exact: true })
    .click();
  await expect(dialog.getByTestId("export-flows/orders.yaml")).toContainText(
    "status: 999",
  );
  await dialog.getByRole("button", { name: "Open affected node" }).click();
  await expect(builder.inspector()).toBeVisible();
  await expect(
    builder.inspector().getByLabel("HTTP status code", { exact: true }),
  ).toHaveValue("999");
});

test("split export and file import remain usable on mobile", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(inline);
  const dialog = await builder.openConfigItem("Export YAML");
  await choose(dialog, "YAML organization", "One YAML per flow");
  await expect(
    dialog.getByRole("button", { name: "Download hookway-config.zip" }),
  ).toBeEnabled();
  const bounds = await dialog.boundingBox();
  expect(bounds!.width).toBeLessThanOrEqual(390);
  await page.screenshot({
    path: testInfo.outputPath("split-export-mobile.png"),
    fullPage: true,
  });
});
