import { expect, test } from "vitest";
import { DEST_TYPES, DEST_TYPE_SPECS, defaultSettings, defaultScheme, defaultsFor } from "./destTypes";
import type { FieldSpec } from "./destTypes";

// Throwaway spec used only to exercise the generic "group" field mechanism;
// no real destination type has a group field yet.
const GROUP_CHILD_FIELDS: FieldSpec[] = [
  { key: "max_attempts", label: "Max attempts", kind: "number" },
  { key: "backoff", label: "Backoff", default: "1s" },
];
DEST_TYPE_SPECS.__group_test__ = {
  block: "__group_test__",
  fields: [
    { key: "name", label: "Name" },
    { key: "retry", label: "Retry", kind: "group", fields: GROUP_CHILD_FIELDS },
  ],
};

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

test("defaultsFor seeds a nested object with child defaults for a group field", () => {
  expect(defaultsFor([{ key: "retry", label: "Retry", kind: "group", fields: GROUP_CHILD_FIELDS }])).toEqual({
    retry: { max_attempts: "", backoff: "1s" },
  });
});

test("defaultSettings recurses into a group field via defaultsFor", () => {
  expect(defaultSettings("__group_test__")).toEqual({
    name: "",
    retry: { max_attempts: "", backoff: "1s" },
  });
});
