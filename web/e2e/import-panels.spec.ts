import { test, expect } from "@playwright/test";
import { BuilderPage } from "./builder";
import {
  destinationNames,
  destEntry,
  exampleFixtures,
  flowNames,
  flowNodes,
  flowPolicy,
  SERVER_LABELS,
  serverConfig,
} from "./examples";
import { expectYamlValue } from "./fields";

for (const fixture of exampleFixtures()) {
  test(`imported ${fixture.name} appears in builder panels`, async ({ page }) => {
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(fixture.yaml);

    const names = flowNames(fixture.config);
    expect(names.length, `${fixture.name} should declare flows`).toBeGreaterThan(0);
    await expect(page.getByRole("button", { name: "Active flow" })).toHaveText(names[0]);
    await builder.expectFlows(names);

    for (const flow of names) {
      await builder.selectFlow(flow);
      await expect(builder.inspector().getByLabel("Flow name")).toHaveValue(flow);

      const policy = flowPolicy(fixture.config, flow);
      const policyControl = builder.inspector().getByLabel("Delivery failure policy");
      if (policy === "skip_failed") {
        await expect(policyControl).toHaveValue("skip_failed");
      } else if (policy === "stop_on_failure") {
        await expect(policyControl).toHaveValue("stop_on_failure");
      } else {
        await expect(policyControl).toHaveValue("stop_on_failure (default)");
      }

      const nodes = flowNodes(fixture.config, flow);
      expect(nodes.map((n) => n.id)).not.toHaveLength(0);
      for (const node of nodes) {
        // exact: true is required -- accessible-name matching is substring by
        // default, so "Select body" also matches a sibling "Select body2"
        // node and trips Playwright's strict mode.
        await expect(
          builder.inspector().getByRole("button", { name: `Select ${node.id}`, exact: true }),
        ).toBeVisible();
      }

      for (const node of nodes) {
        await builder.selectNode(node.id);
        await expect(builder.inspector().getByLabel("Id", { exact: true })).toHaveValue(node.id);
        await expect(builder.inspector().getByLabel("Description")).toHaveValue(node.description);
        await expect(builder.inspector().getByText(node.type, { exact: true }).first()).toBeVisible();
        for (const [key, value] of Object.entries(node.options)) {
          await expectYamlValue(builder.inspector(), key, value);
        }
        await builder.backToFlow();
      }
    }

    const destNames = destinationNames(fixture.config);
    const destDialog = await builder.openConfigItem("Destinations");
    await expect(destDialog.getByRole("heading", { name: "Destinations" })).toBeVisible();
    for (const name of destNames) {
      const card = destDialog.getByTestId(`destination-${name}`);
      await expect(card).toBeVisible();
      await expect(card.getByLabel("Name", { exact: true })).toHaveValue(name);
      const dest = destEntry(fixture.config, name);
      await expect(card.getByLabel("Type", { exact: true })).toHaveValue(dest.type);
      for (const [key, value] of Object.entries(dest.block)) {
        await expectYamlValue(card, key, value);
      }
    }
    await builder.closeDialog();

    const serverDialog = await builder.openConfigItem("Server");
    const server = serverConfig(fixture.config);
    for (const [key, value] of Object.entries(server)) {
      const label = SERVER_LABELS[key];
      if (!label || value === undefined) continue;
      await expectLabeled(serverDialog, label, String(value));
    }
    await builder.closeDialog();

    const envDialog = await builder.openConfigItem("Environment");
    if (fixture.envVars.length === 0) {
      await expect(envDialog.getByText("No environment variables referenced.")).toBeVisible();
    } else {
      for (const name of fixture.envVars) {
        await expect(envDialog.getByText(name, { exact: true })).toBeVisible();
      }
    }
    await builder.closeDialog();
  });
}

async function expectLabeled(root: import("@playwright/test").Locator, label: string, expected: string) {
  const control = root.getByLabel(label, { exact: true });
  await expect.soft(control).toBeVisible();
  const value = await control.inputValue().catch(() => "");
  const text = (await control.innerText().catch(() => "")).trim();
  expect.soft(value || text, `server field "${label}"`).toBe(expected);
}
