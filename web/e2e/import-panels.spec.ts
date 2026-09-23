import { test, expect } from "@playwright/test";
import { BuilderPage } from "./builder";
import {
  connectionNames,
  connEntry,
  destinationNames,
  destEntry,
  exampleFixtures,
  flowNames,
  flowNodes,
  flowPolicy,
  humanLabel,
  isRecord,
  SERVER_LABELS,
  serverConfig,
} from "./examples";
import { expectYamlValue } from "./fields";

// The control is a Select, so it renders the option label, not the config
// value. Keep these in step with the `data` list in Inspector.tsx -- they
// drifted apart once already, and the mismatch failed every fixture at once.
const POLICY_LABELS: Record<string, string> = {
  "": "Use default (stop on first failure)",
  stop_on_failure: "Stop on first failure",
  skip_failed: "Continue after failures",
};

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
      await expect(policyControl).toHaveValue(POLICY_LABELS[policy] ?? POLICY_LABELS[""]);

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
        // The inspector shows the humanised label ("http trigger"), not the
        // YAML type. This mirrors nodeLabel() in src/graph/nodeSchema.ts,
        // which the e2e loader cannot import -- that module pulls in
        // nodeSchema.json, and Playwright's ESM loader rejects the bare
        // JSON import.
        await expect(builder.inspector().getByText(humanLabel(node.type), { exact: true }).first()).toBeVisible();
        for (const [key, value] of Object.entries(node.options)) {
          await expectYamlValue(builder.inspector(), key, value);
        }
        await builder.backToFlow();
      }
    }

    const connNames = connectionNames(fixture.config);
    if (connNames.length > 0) {
      const connDialog = await builder.openConfigItem("Connections");
      await expect(connDialog.getByRole("heading", { name: "Connections" })).toBeVisible();
      for (const name of connNames) {
        await connDialog.getByRole("button", { name: `Select ${name}`, exact: true }).click();
        const card = connDialog.getByTestId(`connection-${name}`);
        await expect(card).toBeVisible();
        await expect(card.getByLabel("Name", { exact: true })).toHaveValue(name);
        const conn = connEntry(fixture.config, name);
        await expect(card.getByLabel("Type", { exact: true })).toHaveValue(conn.type);
        for (const [key, value] of Object.entries(conn.fields)) {
          if (!isRecord(value)) continue;
          await card.getByRole("button", { name: new RegExp(`^${humanLabel(key)}`) }).click();
        }
        for (const [key, value] of Object.entries(conn.fields)) {
          await expectYamlValue(card, key, value);
        }
      }
      await builder.closeDialog();
    }

    const destNames = destinationNames(fixture.config);
    const destDialog = await builder.openConfigItem("Destinations");
    await expect(destDialog.getByRole("heading", { name: "Destinations" })).toBeVisible();
    // The panel is master-detail: the rail lists every destination, the pane
    // holds one. Each has to be selected before its fields exist in the DOM.
    for (const name of destNames) {
      await destDialog.getByRole("button", { name: `Select ${name}`, exact: true }).click();
      const card = destDialog.getByTestId(`destination-${name}`);
      await expect(card).toBeVisible();
      await expect(card.getByLabel("Name", { exact: true })).toHaveValue(name);
      const dest = destEntry(fixture.config, name);
      await expect(card.getByLabel("Type", { exact: true })).toHaveValue(dest.type);
      // Nested blocks (tls, retry, dead_letter, ...) are folded and mount their
      // fields only once open, so expand the ones this fixture asserts on. The
      // control's name carries a "2 set" badge after the label, hence the
      // prefix match rather than an exact one.
      for (const [key, value] of Object.entries(dest.block)) {
        if (!isRecord(value)) continue;
        await card.getByRole("button", { name: new RegExp(`^${humanLabel(key)}`) }).click();
      }
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
