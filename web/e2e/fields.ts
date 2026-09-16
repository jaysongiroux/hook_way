import { expect, type Locator } from "@playwright/test";
import { humanLabel, isRecord } from "./examples";

function arrayOfObjects(value: unknown[]): boolean {
  return value.some((item) => isRecord(item));
}

function formatArray(value: unknown[]): string {
  return value.map(String).join(", ");
}

async function expectSwitch(root: Locator, label: string, checked: boolean, nth: number) {
  const control = root.getByRole("switch", { name: label, exact: true }).nth(nth);
  if (checked) await expect.soft(control).toBeChecked();
  else await expect.soft(control).not.toBeChecked();
}

async function expectLabeled(root: Locator, label: string, expected: string, nth: number) {
  const control = root.getByLabel(label, { exact: true }).nth(nth);
  await expect.soft(control).toBeVisible();
  const value = await control.inputValue().catch(() => "");
  const text = (await control.innerText().catch(() => "")).trim();
  const actual = value || text;
  expect.soft(actual, `field "${label}"`).toBe(expected);
}

/** Assert a YAML value as the builder's SchemaForm would render it. */
export async function expectYamlValue(root: Locator, key: string, value: unknown, nth = 0): Promise<void> {
  if (value === undefined) return;
  const label = humanLabel(key);

  if (Array.isArray(value)) {
    if (arrayOfObjects(value)) {
      const group = root.getByRole("group", { name: label, exact: true }).nth(nth);
      await expect.soft(group).toBeVisible();
      for (let i = 0; i < value.length; i++) {
        const row = value[i];
        if (!isRecord(row)) continue;
        for (const [childKey, childValue] of Object.entries(row)) {
          await expectYamlValue(group, childKey, childValue, i);
        }
      }
      return;
    }
    await expectLabeled(root, label, formatArray(value), nth);
    return;
  }

  if (isRecord(value)) {
    const group = root.getByRole("group", { name: label, exact: true }).nth(nth);
    await expect.soft(group).toBeVisible();
    const mapKey = group.getByLabel("Key", { exact: true });
    if ((await mapKey.count()) > 0) {
      const entries = Object.entries(value);
      const inputs = group.locator("input");
      await expect.soft(inputs).toHaveCount(entries.length * 2);
      for (let i = 0; i < entries.length; i++) {
        await expect.soft(inputs.nth(i * 2)).toHaveValue(entries[i][0]);
        await expect.soft(inputs.nth(i * 2 + 1)).toHaveValue(String(entries[i][1] ?? ""));
      }
      return;
    }
    for (const [childKey, childValue] of Object.entries(value)) {
      await expectYamlValue(group, childKey, childValue, 0);
    }
    return;
  }

  if (typeof value === "boolean") {
    const switches = root.getByRole("switch", { name: label, exact: true });
    if ((await switches.count()) > nth) {
      await expectSwitch(root, label, value, nth);
      return;
    }
    await expectLabeled(root, label, String(value), nth);
    return;
  }

  await expectLabeled(root, label, String(value), nth);
}
