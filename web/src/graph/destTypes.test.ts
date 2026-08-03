import { expect, test } from "vitest";
import { DEST_TYPES, DEST_TYPE_SPECS, defaultSettings, defaultScheme } from "./destTypes";

test("DEST_TYPES lists the real 12 backend types without a bare redis", () => {
  expect(DEST_TYPES).toContain("rabbitmq");
  expect(DEST_TYPES).toContain("redis_stream");
  expect(DEST_TYPES).not.toContain("redis");
  expect(DEST_TYPES).toHaveLength(12);
});

test("every type has a spec with a block name and fields", () => {
  for (const t of DEST_TYPES) {
    expect(DEST_TYPE_SPECS[t]).toBeDefined();
    expect(typeof DEST_TYPE_SPECS[t].block).toBe("string");
    expect(DEST_TYPE_SPECS[t].fields.length).toBeGreaterThan(0);
  }
});

test("redis_* types all nest under the redis block", () => {
  for (const t of ["redis_stream", "redis_list", "redis_pubsub", "redis_kv"]) {
    expect(DEST_TYPE_SPECS[t].block).toBe("redis");
  }
});

test("defaultSettings seeds enum/bool defaults", () => {
  expect(defaultSettings("rabbitmq")).toMatchObject({ url: "", exchange: "", exchange_type: "direct", durable: true, declare_queues: true, publish_timeout: "" });
  expect(defaultSettings("kafka")).toMatchObject({ acks: "all", compression: "none" });
  expect(defaultSettings("redis_stream")).toMatchObject({ mode: "single", url: "" });
});

test("SCHEMES_BY_AUTH + defaultScheme", () => {
  expect(defaultScheme("hmac_sha256")).toBe("hmac_sha256_hex");
  expect(defaultScheme("jwt")).toBe("jwt_hs256");
  expect(defaultScheme("none")).toBe("");
});

test("select/bool fields carry options/kind", () => {
  const et = DEST_TYPE_SPECS.rabbitmq.fields.find((f) => f.key === "exchange_type")!;
  expect(et.kind).toBe("select");
  expect(et.options).toEqual(["direct", "fanout", "topic", "headers"]);
  expect(DEST_TYPE_SPECS.rabbitmq.fields.find((f) => f.key === "durable")!.kind).toBe("bool");
});
