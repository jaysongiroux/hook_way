import { expect, test } from "vitest";
import { parseSchemaObject, precisionRisk } from "./jsonPrecision";

test("schema JSON parsing keeps representable numbers and rejects rounded literals", () => {
  expect(parseSchemaObject('{"const":9007199254740992}')).toEqual({ const: 9007199254740992 });
  expect(parseSchemaObject('{"multipleOf":0.1}')).toEqual({ multipleOf: 0.1 });
  expect(precisionRisk('{"const":9007199254740993}')).toMatch(/9007199254740993/);
  expect(precisionRisk('{"const":0.123456789012345678}')).toMatch(/precision/);
  expect(precisionRisk('{"const":1e999}')).toMatch(/precision/);
  expect(precisionRisk('{"const":-0}')).toMatch(/precision/);
});

test("numeric text inside schema strings is not treated as a number", () => {
  expect(parseSchemaObject('{"description":"9007199254740993 and \\"1e999\\""}'))
    .toEqual({ description: '9007199254740993 and "1e999"' });
});

test("schema must be a nonempty object", () => {
  expect(() => parseSchemaObject("{}")).toThrow(/nonempty/);
  expect(() => parseSchemaObject("[]")).toThrow(/object/);
});
