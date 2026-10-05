import { expect, test } from "vitest";
import { parseYamlMapping } from "./fromYaml";

for (const type of ["json_validate", "transform"]) {
  const option = type === "json_validate" ? "schema" : "json_schema";
  const kind = type === "transform" ? "kind: json_schema," : "";
  test(`${type} does not silently round imported schema numbers`, () => {
    for (const literal of ["9007199254740993", "0.123456789012345678901", "1.234567890123456789e3", "1e309", "1e-9999", "0x20000000000001"]) {
      expect(() => parseYamlMapping(`flows: {test: {nodes: [{id: check, type: ${type}, ${kind} ${option}: {const: ${literal}}}]}}`, "Core config")).toThrow(/lose precision/);
    }
  });
  test(`${type} retains exact and quoted numeric constraints`, () => {
    for (const literal of ["9007199254740992", "1024", "0x100", "0.5", '"9007199254740993"']) {
      expect(() => parseYamlMapping(`flows: {test: {nodes: [{id: check, type: ${type}, ${kind} ${option}: {const: ${literal}}}]}}`, "Core config")).not.toThrow();
    }
  });
}

test("schema numeric YAML syntax cannot silently change constraint types", () => {
  for (const literal of ["!!float 9007199254740993", "1_024", "1_000.25", "012"]) {
    expect(() => parseYamlMapping(`flows: {test: {nodes: [{type: json_validate, schema: {const: ${literal}}}]}}`, "Core config")).toThrow(/numeric literal|leading-zero/);
  }
  expect(() => parseYamlMapping("flows: {test: {nodes: [{type: json_validate, schema: {const: !!str 1e309}}]}}", "Core config")).not.toThrow();
});

test("schema aliases are checked before conversion", () => {
  expect(() => parseYamlMapping("constraint: &s {const: 9007199254740993}\nflows: {test: {nodes: [{type: json_validate, schema: *s}]}}", "Core config")).toThrow(/lose precision/);
});
