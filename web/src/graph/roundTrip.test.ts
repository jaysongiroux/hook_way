import { expect, test } from "vitest";
import { parse } from "yaml";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { layout } from "./layout";

const examplesDir = join(dirname(fileURLToPath(import.meta.url)), "../../../examples/showcase");

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(value as object).sort()) {
      out[k] = normalize((value as Record<string, unknown>)[k]);
    }
    return out;
  }
  return value;
}

test.each(readdirSync(examplesDir).filter((f) => f.endsWith(".yaml")))("round-trips %s without field loss", (file) => {
  const yaml = readFileSync(join(examplesDir, file), "utf8");
  const parsed = parse(yaml) as Record<string, unknown>;
  const model = fromConfig(parsed);
  const { config } = toConfig({ ...model, nodes: layout(model.nodes, model.edges) });
  expect(normalize(config)).toEqual(normalize(parsed));
});

// examples/multipart.yaml is outside the showcase sweep above, but its nested
// `multipart:` option block is the first nested object on a non-destination
// node — if the builder's import/export path flattened or dropped it, the
// block an operator typed in the inspector would vanish on export.
test("round-trips the multipart example, nested option block included", () => {
  const path = join(dirname(fileURLToPath(import.meta.url)), "../../../examples/multipart.yaml");
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const model = fromConfig(parsed);
  const { config } = toConfig({ ...model, nodes: layout(model.nodes, model.edges) });
  // Named explicitly so the test fails loudly if the block is ever dropped,
  // rather than passing by comparing two configs that both lost it.
  expect(JSON.stringify(config)).toContain("max_part_bytes");
  expect(normalize(config)).toEqual(normalize(parsed));
});
