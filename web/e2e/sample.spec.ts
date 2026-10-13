import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const fixture = readFileSync(new URL("../../examples/sample.yaml", import.meta.url), "utf8");

test("sample creation, decimal persistence and real WASM validation", async ({ page }, testInfo) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  const nodes = page.getByRole("button", { name: "Nodes", exact: true });
  if ((await nodes.getAttribute("aria-pressed")) !== "true") await nodes.click();
  await page.getByRole("button", { name: "Add Sample requests", exact: true }).click();
  await expect(builder.inspector()).toBeVisible();
  await expect(builder.inspector().getByLabel("Sampling rate", { exact: true })).toHaveValue("1");
  await expect(builder.inspector()).toContainText("YAML type: sample");

  await builder.importYaml(fixture);
  for (const value of ["0", "1", "0.235", "0.001", "0.00001"]) {
    await builder.selectNode("sample_events");
    const input = builder.inspector().getByLabel("Sampling rate", { exact: true });
    await input.fill("");
    await input.pressSequentially(value);
    await input.press("Tab");
    const dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-warnings")).toContainText("has no rate_limit node");
    await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
    await expect(dialog.getByRole("button", { name: "Download core.yaml" })).toBeEnabled();
    const output = parse(await dialog.getByTestId("export-core.yaml").innerText());
    expect(output.flows.sampled_events.nodes.find((node: { id: string }) => node.id === "sample_events")).toMatchObject({ type: "sample", rate: Number(value) });
    await builder.closeDialog();
    await page.reload();
    await builder.selectNode("sample_events");
    await expect(builder.inspector().getByLabel("Sampling rate", { exact: true })).toHaveValue(value);
  }

  for (const value of ["1.01", "-0.01", ""]) {
    await builder.selectNode("sample_events");
    const input = builder.inspector().getByLabel("Sampling rate", { exact: true });
    await input.fill(value);
    await input.press("Tab");
    await expect(input).toHaveAttribute("aria-invalid", "true");
    const dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-errors")).toContainText(value === "" ? "rate is required" : "rate must be a finite number between 0 and 1 inclusive");
    await expect(dialog.getByRole("button", { name: "Download core.yaml" })).toBeDisabled();
    await builder.closeDialog();
  }
  await builder.selectNode("sample_events");
  const repaired = builder.inspector().getByLabel("Sampling rate", { exact: true });
  await repaired.fill("0.2");
  await repaired.press("Tab");
  await expect(builder.inspector().getByText(/Checking configuration/)).toBeHidden();
  await expect(repaired).not.toHaveAttribute("aria-invalid", "true");
  await page.screenshot({ path: testInfo.outputPath("sample-builder.png") });
});
