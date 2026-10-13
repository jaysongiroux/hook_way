import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");
const coreYaml = readFileSync(join(root, "examples/replay/core.yaml"), "utf8");
const replayYaml = readFileSync(join(root, "examples/replay/replay.yaml"), "utf8");

test("imports, edits, persists, and exports both replay documents", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(coreYaml);
  await builder.importReplayYaml(replayYaml);

  let dialog = await builder.openConfigItem("Core server");
  await dialog.getByLabel("Address", { exact: true }).fill(":8181");
  await builder.closeDialog();

  dialog = await builder.openConfigItem("Replay service");
  await expect(dialog.getByLabel("capture token")).toHaveValue("${REPLAY_CAPTURE_TOKEN}");
  await dialog.getByRole("tab", { name: "Replay service" }).click();
  await dialog.getByRole("button", { name: /^Retention/ }).click();
  await dialog.getByLabel("ttl", { exact: true }).fill("336h");
  await builder.closeDialog();

  await page.waitForTimeout(500);
  await page.reload();
  await builder.gotoReady();
  dialog = await builder.openConfigItem("Replay service");
  await dialog.getByRole("tab", { name: "Replay service" }).click();
  await dialog.getByRole("button", { name: /^Retention/ }).click();
  await expect(dialog.getByLabel("ttl", { exact: true })).toHaveValue("336h");
  await builder.closeDialog();

  dialog = await builder.openConfigItem("Export YAML");
  const core = parse(await dialog.getByTestId("export-core.yaml").innerText()) as Record<string, unknown>;
  expect((core.server as Record<string, unknown>).addr).toBe(":8181");
  expect(core.replay).toBeTruthy();
  await dialog.getByRole("tab", { name: "Replay YAML" }).click();
  const replay = parse(await dialog.getByTestId("export-replay.yaml").innerText()) as Record<string, unknown>;
  expect((replay.retention as Record<string, unknown>).ttl).toBe("336h");
});

test("replay settings stay usable at a compact viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  const dialog = await builder.openConfigItem("Replay service");
  await expect(dialog.getByRole("button", { name: "Add replay configuration" })).toBeVisible();
  await expect(dialog).toBeInViewport();
});
