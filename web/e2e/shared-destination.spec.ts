import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const fixture = readFileSync(new URL("../../examples/shared-destination.yaml", import.meta.url), "utf8");

test("two canvas connections to one destination persist and pass WASM validation", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(fixture.replace("      - left>send\n", "").replace("      - right>send\n", ""));
  await expect(page.locator(".react-flow__edge")).toHaveCount(5);
  // React Flow's handles have no accessible role; scope these DOM locators to
  // the visible node ID, then use the handle IDs that define the connection.
  const node = (id: string) => page.locator(".react-flow__node").filter({ has: page.getByText(id, { exact: true }) });
  for (const [index, id] of ["left", "right"].entries()) {
    const source = node(id).locator('.react-flow__handle.source[data-handleid="out"]');
    const target = node("send").locator('.react-flow__handle.target[data-handleid="in"]');
    await source.click();
    await target.click();
    await expect(page.locator(".react-flow__edge")).toHaveCount(6 + index);
  }
  const exportAndCheck = async () => {
    const dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-warnings")).toContainText("has no rate_limit node");
    await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
    await expect(dialog.getByRole("button", { name: "Download core.yaml" })).toBeEnabled();
    const text = await dialog.getByTestId("export-core.yaml").innerText();
    const yaml = parse(text);
    expect(yaml.flows.shared_destination.edges).toEqual(expect.arrayContaining(["left>send", "right>send"]));
    return text;
  };
  const exported = await exportAndCheck();
  await builder.closeDialog();
  await page.reload();
  await expect(page.locator(".react-flow__edge")).toHaveCount(7);
  await exportAndCheck();
  await builder.closeDialog();
  await builder.importYaml(exported);
  await expect(page.locator(".react-flow__edge")).toHaveCount(7);
  await exportAndCheck();
});

test("conditional outputs share one destination through real WASM", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  const conditional = fixture
    .replace("      - {id: left, type: transform, kind: strip_fields, fields: [internal_notes]}\n      - {id: right, type: transform, kind: strip_fields, fields: [customer_email]}",
      "      - {id: choose, type: if_else, conditions: [{source: body, path: type, operator: eq, value: created}]}")
    .replace("      - env>left\n      - env>right\n      - left>send\n      - right>send", "      - env>choose\n      - choose:true>send\n      - choose:false>send");
  await builder.importYaml(conditional);
  const dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-warnings")).toContainText("has no rate_limit node");
  await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Download core.yaml" })).toBeEnabled();
});
