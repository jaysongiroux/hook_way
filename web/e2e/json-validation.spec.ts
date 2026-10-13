import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { BuilderPage } from "./builder";

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "../..");
const example = readFileSync(join(repoRoot, "examples/json-validation.yaml"), "utf8");

test("real Go WASM checks large numbers, schema constraints, local references, and conversions", async ({ page }) => {
  await page.goto("/builder");
  await expect.poll(() => page.evaluate(() => typeof (window as unknown as { hookwayJSONSampleValidate?: unknown }).hookwayJSONSampleValidate), {
    timeout: 15_000,
  }).toBe("function");
  const results = await page.evaluate(() => {
    const call = (window as unknown as { hookwayJSONSampleValidate: (json: string) => string }).hookwayJSONSampleValidate;
    const schema = {
      type: "object", required: ["order_id", "customer"], additionalProperties: false,
      properties: { order_id: { type: "integer" }, customer: { $ref: "#/definitions/customer" } },
      definitions: { customer: { type: "object", required: ["email"], properties: { email: { type: "string", format: "email" } }, additionalProperties: false } },
    };
    const validate = (sample: string, coercions: { path: string; type: string }[] = []) =>
      JSON.parse(call(JSON.stringify({ schema, sample, coercions }))) as {
        valid: boolean; output?: string; issues: { field: string; keyword: string }[];
      };
    return {
      valid: validate('{"order_id":9007199254740993,"customer":{"email":"dev@example.com"}}'),
      required: validate('{"order_id":1}'),
      null: validate('{"order_id":null,"customer":{"email":"dev@example.com"}}'),
      format: validate('{"order_id":1,"customer":{"email":"bad"}}'),
      extra: validate('{"order_id":1,"customer":{"email":"dev@example.com"},"extra":1}'),
      localRef: validate('{"order_id":1,"customer":{}}'),
      converted: validate('{"order_id":"42","customer":{"email":"dev@example.com"}}', [{ path: "order_id", type: "integer" }]),
      badConversion: validate('{"order_id":"42","customer":{"email":"dev@example.com"}}', [{ path: "order_id", type: "string" }]),
      bounded: JSON.parse(call(JSON.stringify({
        schema: { allOf: Array.from({ length: 200 }, () => ({ pattern: "^a+$" })) },
        sample: JSON.stringify("a".repeat(250000)),
      }))) as { valid: boolean; issues: { keyword: string }[] },
    };
  });
  expect(results.valid.valid).toBe(true);
  expect(results.valid.output).toContain("9007199254740993");
  for (const key of ["required", "null", "format", "extra", "localRef"] as const) {
    expect(results[key].valid, key).toBe(false);
    expect(results[key].issues.length, key).toBeGreaterThan(0);
  }
  expect(results.converted.valid).toBe(true);
  expect(results.converted.output).toContain('"order_id":42');
  expect(results.badConversion.valid).toBe(false);
  expect(results.badConversion.issues.some((issue) => issue.field === "coercions")).toBe(true);
  expect(results.bounded.valid).toBe(false);
  expect(results.bounded.issues[0].keyword).toBe("limit");
});

test("dedicated editor retains drafts, tests raw samples, and exports only runtime options", async ({ page }, testInfo) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(example);
  await builder.selectNode("check");
  const inspector = builder.inspector();
  const schema = inspector.getByLabel("JSON Schema");
  const originalSchema = await schema.inputValue();
  await schema.fill('{"minimum":9007199254740993}');
  await expect(schema).toHaveAttribute("aria-invalid", "true");
  await builder.closeInspector();
  await builder.selectNode("check");
  await expect(builder.inspector().getByLabel("JSON Schema")).toHaveValue('{"minimum":9007199254740993}');
  await page.reload();
  await builder.gotoReady();
  await builder.selectNode("check");
  await expect(builder.inspector().getByLabel("JSON Schema")).toHaveValue('{"minimum":9007199254740993}');
  await builder.inspector().getByLabel("JSON Schema").fill(originalSchema);
  const precise = '{"order_id":9007199254740993,"customer":{"email":"dev@example.com"}}';
  await builder.inspector().getByLabel("JSON sample").fill(precise);
  await builder.inspector().getByRole("button", { name: "Validate sample" }).click();
  await expect(builder.inspector().getByText("Sample is valid")).toBeVisible();
  await expect(builder.inspector().locator("pre")).toContainText("9007199254740993");
  await builder.inspector().getByRole("button", { name: "Add conversion" }).click();
  await builder.inspector().getByLabel("Property path 1").fill("order_id");
  await builder.inspector().getByLabel("Convert to 1").selectOption("integer");
  await builder.inspector().getByLabel("JSON sample").fill('{"order_id":"42","customer":{"email":"dev@example.com"}}');
  await expect(builder.inspector().getByText("Sample is valid")).toHaveCount(0);
  await builder.inspector().getByRole("button", { name: "Validate sample" }).click();
  await expect(builder.inspector().getByText("Sample is valid")).toBeVisible();
  await expect(builder.inspector().locator("pre")).toContainText('"order_id":42');
  await expect(builder.inspector()).toHaveCSS("opacity", "1");
  await page.screenshot({ path: testInfo.outputPath("json-validation-wide.png"), animations: "disabled" });
  await builder.inspector().getByLabel("JSON sample").fill('{"order_id":');
  await builder.closeInspector();
  await builder.selectNode("check");
  await expect(builder.inspector().getByLabel("JSON sample")).toHaveValue('{"order_id":');
  const dialog = await builder.openConfigItem("Export YAML");
  const yaml = await dialog.getByTestId("export-core.yaml").innerText();
  const config = parse(yaml) as { flows: { orders: { nodes: Record<string, unknown>[] } } };
  const check = config.flows.orders.nodes.find((node) => node.id === "check")!;
  expect(check.schema).toMatchObject({ type: "object", required: ["order_id", "customer"] });
  expect(check.coercions).toEqual([{ path: "order_id", type: "integer" }]);
  expect(yaml).not.toContain("_json_sample");
  expect(yaml).not.toContain('"order_id":');
});

test("legacy JSON Schema transform opens the same editor", async ({ page }) => {
  const legacy = example.replace("type: json_validate\n        schema:", "type: transform\n        kind: json_schema\n        json_schema:");
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(legacy);
  await builder.selectNode("check");
  await expect(builder.inspector().getByLabel("JSON Schema")).toBeVisible();
  await expect(builder.inspector().getByRole("button", { name: "Add conversion" })).toBeVisible();
  await expect(builder.inspector().getByLabel("JSON sample")).toBeVisible();
});

test("conversion controls stay within a compact inspector", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(example);
  await builder.selectNode("check");
  const inspector = builder.inspector();
  await inspector.getByRole("button", { name: "Add conversion" }).click();
  for (const locator of [
    inspector.getByLabel("Property path 1"),
    inspector.getByLabel("Convert to 1"),
    inspector.getByRole("button", { name: "Remove conversion 1" }),
  ]) {
    const box = await locator.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  }
  await inspector.getByLabel("Property path 1").fill("order_id");
  await inspector.getByLabel("Convert to 1").selectOption("integer");
  await expect(inspector).toHaveCSS("opacity", "1");
  await page.screenshot({ path: testInfo.outputPath("json-validation-narrow.png"), animations: "disabled" });
});
