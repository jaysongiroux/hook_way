import { expect, test } from "vitest";
import {
  NODE_SCHEMAS,
  NODE_TYPES,
  DEST_TYPES,
  CONN_TYPES,
  connSchema,
  connTypeForDest,
  connOwnedPaths,
  schemaRequired,
  schemaType,
  schemaProperties,
} from "./nodeSchema";

test("every catalog node type has palette metadata", () => {
  expect(NODE_TYPES).toHaveLength(18);
  for (const type of NODE_TYPES) {
    const meta = NODE_SCHEMAS[type];
    expect(meta.input, type).toBeTruthy();
    expect(meta.output, type).toBeTruthy();
    expect(meta.schema, type).toBeTruthy();
    expect(typeof meta.on_error, type).toBe("boolean");
  }
  expect(NODE_SCHEMAS.http_trigger.on_error).toBe(false);
  expect(NODE_SCHEMAS.read_body.on_error).toBe(false);
  expect(NODE_SCHEMAS.no_auth.on_error).toBe(false);
  // if_else cannot fail: a condition that does not hold is the false branch.
  expect(NODE_SCHEMAS.if_else.on_error).toBe(false);
  expect(NODE_SCHEMAS.rate_limit.on_error).toBe(true);
  expect(NODE_SCHEMAS.auth.on_error).toBe(true);
  expect(NODE_SCHEMAS.envelope.on_error).toBe(true);
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

test("publish declares Envelope output so a success continuation can type-check", () => {
  expect(NODE_SCHEMAS.publish.output).toBe("Envelope");
  expect(NODE_SCHEMAS.publish.detached).toBe(true);
  expect(NODE_SCHEMAS.respond.output).toBe("Terminal");
});

test("destination catalog lists the types the marketing page counts", () => {
  expect(DEST_TYPES).toHaveLength(14);
  expect(DEST_TYPES).toContain("webhook");
  expect(DEST_TYPES).toContain("redis_stream");
  expect(DEST_TYPES).toContain("lambda");
});

test("every connection type in the generated catalog has fields", () => {
  expect(CONN_TYPES).toEqual(["aws", "kafka", "mongodb", "postgres", "rabbitmq", "redis", "temporal"]);
  for (const type of CONN_TYPES) {
    expect(Object.keys(schemaProperties(connSchema(type))).length, type).toBeGreaterThan(0);
  }
});

// CONN_TYPE_FOR_DEST mirrors connTypeForDestType in Go. If the two drift, the
// builder offers a connection the loader will reject (or hides one it accepts).
test("every destination type except webhook maps to a real connection type", () => {
  for (const dest of DEST_TYPES) {
    const conn = connTypeForDest(dest);
    if (dest === "webhook") {
      expect(conn, "a webhook credential carries no host, so it is not a connection").toBeUndefined();
      continue;
    }
    expect(conn, dest).toBeDefined();
    expect(CONN_TYPES, dest).toContain(conn);
  }
});

test("connOwnedPaths walks into nested blocks", () => {
  const aws = connOwnedPaths("aws");
  expect(aws).toEqual(["region", "endpoint", "access_key_id", "secret_access_key"]);
  const pg = connOwnedPaths("postgres");
  expect(pg).toContain("tls");
  expect(pg).toContain("tls.client_key");
  // tls.enabled is a redis/kafka/temporal field; postgres TLS follows the DSN.
  expect(pg).not.toContain("tls.enabled");
  expect(connOwnedPaths("redis")).toContain("tls.enabled");
});
