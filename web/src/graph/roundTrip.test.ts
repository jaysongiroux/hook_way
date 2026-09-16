import { expect, test } from "vitest";
import { parse } from "yaml";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { layout } from "./layout";

const examplesDir = join(dirname(fileURLToPath(import.meta.url)), "../../../examples/v2");

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
