import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { fromReplayYaml, toReplayYaml } from "./replayYaml";

const examples = join(dirname(fileURLToPath(import.meta.url)), "../../../examples");

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as Record<string, unknown>).sort().map(([key, child]) => [key, normalize(child)]));
  }
  return value;
}

test.each(["dev/replay.yaml", "replay/replay.yaml"])("round-trips %s without field loss", (name) => {
  const source = readFileSync(join(examples, name), "utf8");
  expect(normalize(parse(toReplayYaml(fromReplayYaml(source))))).toEqual(normalize(parse(source)));
});

test("rejects a non-mapping replay document", () => {
  expect(() => fromReplayYaml("- not\n- a\n- config\n")).toThrow(/must be a YAML mapping/);
  expect(() => fromReplayYaml("# comments only\n")).toThrow(/must be a YAML mapping/);
  expect(() => fromReplayYaml("null\n")).toThrow(/must be a YAML mapping/);
});
