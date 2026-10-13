import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { parse, stringify } from "yaml";
import { BuilderPage } from "./builder";

const config = readFileSync(
  new URL("../../examples/launchdarkly.yaml", import.meta.url),
  "utf8",
);
for (const width of [1440, 390]) {
  test(`LaunchDarkly typed flags configure, export and reimport at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(config);
    await builder.selectNode("evaluate");
    await expect(
      builder.inspector().getByLabel("Context key path", { exact: true }),
    ).toHaveValue("tenant.id");
    await builder
      .inspector()
      .getByRole("button", { name: "Manage launchdarkly", exact: true })
      .click();
    const connections = page.getByRole("dialog", {
      name: "LaunchDarkly connections",
      exact: true,
    });
    await connections
      .getByLabel("SDK key environment variable", { exact: true })
      .fill("TEST_LD_KEY");
    await expect(
      connections.getByLabel("Send evaluation events", { exact: true }),
    ).toBeChecked();
    await page.screenshot({
      path: testInfo.outputPath(`launchdarkly-connections-${width}.png`),
    });
    expect(
      await connections.evaluate((e) => e.scrollWidth - e.clientWidth),
    ).toBeLessThanOrEqual(1);
    await connections
      .getByRole("button", { name: "Close LaunchDarkly connections" })
      .click();
    const inspector = builder.inspector();
    await inspector
      .getByRole("textbox", { name: "Selected flag alias", exact: true })
      .click();
    await page.getByRole("option", { name: "settings", exact: true }).click();
    await expect(
      inspector.getByLabel("Fallback value", { exact: true }),
    ).toHaveValue("null");
    await inspector
      .getByLabel("Fallback value", { exact: true })
      .fill('{"limit":2.5,"enabled":true}');
    await inspector
      .getByRole("textbox", { name: "Selected flag alias", exact: true })
      .click();
    await page.getByRole("option", { name: "limit", exact: true }).click();
    await inspector.getByLabel("Fallback value", { exact: true }).fill("0.001");
    await page.screenshot({
      path: testInfo.outputPath(`launchdarkly-node-${width}.png`),
    });
    expect(
      await inspector.evaluate((e) => e.scrollWidth - e.clientWidth),
    ).toBeLessThanOrEqual(1);
    await builder.selectNode("choose");
    await expect(
      builder.inspector().getByLabel("Condition 1 Source", { exact: true }),
    ).toHaveValue("flags");
    const dialog = await builder.openConfigItem("Export YAML");
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeEnabled();
    await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
    const yaml = await dialog.locator("pre").innerText();
    const exported = parse(yaml);
    expect(exported.launchdarkly.production.sdk_key_env).toBe("TEST_LD_KEY");
    const node = exported.flows.tenant_rollout.nodes.find(
      (n: { id: string }) => n.id === "evaluate",
    );
    expect(node.flags.limit.fallback).toBe(0.001);
    expect(node.flags.settings.fallback).toEqual({ limit: 2.5, enabled: true });
    await builder.importYaml(yaml);
    await builder.selectNode("evaluate");
    await builder
      .inspector()
      .getByRole("textbox", { name: "Selected flag alias", exact: true })
      .click();
    await page.getByRole("option", { name: "limit", exact: true }).click();
    await expect(
      builder.inspector().getByLabel("Fallback value", { exact: true }),
    ).toHaveValue("0.001");
    expect(errors).toEqual([]);
  });
}

const duplicateNamespace = parse(config);
const duplicate = structuredClone(
  duplicateNamespace.flows.tenant_rollout.nodes.find(
    (node: { id: string }) => node.id === "evaluate",
  ),
);
duplicate.id = "evaluate_again";
duplicateNamespace.flows.tenant_rollout.nodes.push(duplicate);
for (const [name, invalid, message] of [
  [
    "unknown connection",
    config.replace("connection: production", "connection: missing"),
    "LaunchDarkly connection is not configured",
  ],
  [
    "wrong fallback type",
    config.replace("fallback: false", "fallback: 'false'"),
    "fallback must match type boolean",
  ],
  [
    "missing fallback",
    config.replace("            fallback: false", ""),
    "fallback is required",
  ],
  [
    "invalid selector",
    config.replace(
      "source: body, path: tenant.id",
      "source: query, path: tenant.id",
    ),
    "source must be body, header or vars",
  ],
  [
    "duplicate namespace",
    stringify(duplicateNamespace),
    "flag result namespace must be unique per flow",
  ],
]) {
  test(`LaunchDarkly browser validation rejects ${name}`, async ({ page }) => {
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(invalid);
    const dialog = await builder.openConfigItem("Export YAML");
    await expect(dialog.getByTestId("validation-errors")).toContainText(
      message,
    );
    await expect(
      dialog.getByRole("button", { name: "Download core.yaml" }),
    ).toBeDisabled();
  });
}
