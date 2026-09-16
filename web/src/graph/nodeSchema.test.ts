import { expect, test } from "vitest";
import { NODE_SCHEMAS, NODE_TYPES, DEST_TYPES, schemaRequired, schemaType, schemaProperties } from "./nodeSchema";

test("every catalog node type has palette metadata", () => {
  expect(NODE_TYPES).toHaveLength(17);
  for (const type of NODE_TYPES) {
    const meta = NODE_SCHEMAS[type];
    expect(meta.input, type).toBeTruthy();
    expect(meta.output, type).toBeTruthy();
    expect(meta.schema, type).toBeTruthy();
  }
});

test("option schemas cover string, number, boolean, enum, array, and nested object", () => {
  expect(schemaType(schemaProperties(NODE_SCHEMAS.http_trigger.schema).path)).toBe("string");
  expect(schemaType(schemaProperties(NODE_SCHEMAS.read_body.schema).max_bytes)).toBe("integer");
  expect(schemaType(schemaProperties(NODE_SCHEMAS.respond.schema).passthrough)).toBe("boolean");
  expect(schemaType(schemaProperties(NODE_SCHEMAS.filter.schema).on_no_match)).toBe("enum");
  expect(schemaType(schemaProperties(NODE_SCHEMAS.filter.schema).conditions)).toBe("array");
  expect(schemaType(schemaProperties(NODE_SCHEMAS.auth.schema).secret_ref)).toBe("object");
});

test("required options are marked on the schema", () => {
  expect(schemaRequired(NODE_SCHEMAS.http_trigger.schema)).toContain("path");
  expect(schemaRequired(NODE_SCHEMAS.publish.schema)).toEqual(expect.arrayContaining(["destination", "target"]));
  expect(schemaRequired(NODE_SCHEMAS.read_body.schema)).toContain("max_bytes");
});

test("destination catalog lists the types the marketing page counts", () => {
  expect(DEST_TYPES).toHaveLength(13);
  expect(DEST_TYPES).toContain("webhook");
  expect(DEST_TYPES).toContain("redis_stream");
});
