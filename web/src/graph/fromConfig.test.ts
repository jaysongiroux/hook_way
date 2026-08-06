import { beforeEach, expect, test } from "vitest";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { createNode, resetIdCounters } from "./model";
import { DEST_TYPE_SPECS } from "./destTypes";
import type { Edge } from "@xyflow/react";

beforeEach(() => resetIdCounters());

// Throwaway spec used only to exercise the generic "group" field mechanism;
// no real destination type has a group field yet.
DEST_TYPE_SPECS.__group_test__ = {
  block: "__group_test__",
  fields: [
    { key: "name", label: "Name" },
    {
      key: "retry",
      label: "Retry",
      kind: "group",
      fields: [
        { key: "max_attempts", label: "Max attempts", kind: "number" },
        { key: "backoff", label: "Backoff" },
      ],
    },
  ],
};

function edge(source: string, target: string, data = { target: "q", create: true }): Edge {
  return { id: `${source}-${target}`, source, target, data };
}

test("round-trips a simple endpoint->destination graph through config", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  (ep.data as any).auth.secret = "GH_SECRET";
  const d = createNode("destination", { x: 0, y: 0 });
  (d.data as any).settings = { url: "amqp://h", exchange: "x", exchange_type: "direct", durable: true, declare_queues: true, publish_timeout: "" };
  const cfg1 = toConfig([ep, d], [edge(ep.id, d.id, { target: "gh", create: true })]).config;

  const { nodes, edges } = fromConfig(cfg1);
  const cfg2 = toConfig(nodes, edges).config;
  expect(cfg2).toEqual(cfg1);
});

test("round-trips endpoint-level + per-route filters (fan-out)", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f = createNode("filter", { x: 0, y: 0 });
  (f.data as any).path = "action";
  (f.data as any).value = "opened";
  const d1 = createNode("destination", { x: 0, y: 0 });
  const d2 = createNode("destination", { x: 0, y: 0 });
  const edges = [edge(ep.id, f.id), edge(f.id, d1.id, { target: "t1", create: true }), edge(f.id, d2.id, { target: "t2", create: true })];
  const cfg1 = toConfig([ep, f, d1, d2], edges).config;

  const rebuilt = fromConfig(cfg1);
  const cfg2 = toConfig(rebuilt.nodes, rebuilt.edges).config;
  expect(cfg2).toEqual(cfg1);
});

test("imports env-secret and no_auth correctly", () => {
  const config = {
    server: { addr: ":8080", max_body_bytes: 1048576 },
    destinations: { d: { type: "rabbitmq", rabbitmq: { url: "u", exchange: "e", exchange_type: "direct", durable: true, declare_queues: true } } },
    endpoints: {
      a: { enabled: true, path: "/a", auth: { type: "hmac_sha256", scheme: "hmac_sha256_hex", signature_header: "X", secret: "${S}" }, destinations: [{ destination: "d", target: "t", create: true }] },
      b: { enabled: true, path: "/b", auth: { type: "no_auth", scheme: "no_auth" }, destinations: [{ destination: "d", target: "t2", create: true }] },
    },
  };
  const { nodes } = fromConfig(config);
  const a = nodes.find((n) => n.data.kind === "endpoint" && (n.data as any).name === "a")!;
  expect((a.data as any).auth).toMatchObject({ secretMode: "env", secret: "S", scheme: "hmac_sha256_hex" });
  const b = nodes.find((n) => n.data.kind === "endpoint" && (n.data as any).name === "b")!;
  expect((b.data as any).auth.type).toBe("none");
});

test("same-name endpoint and destination get distinct ids", () => {
  const config = {
    destinations: { shared: { type: "rabbitmq", rabbitmq: { url: "u", exchange: "e", exchange_type: "direct" } } },
    endpoints: {
      shared: {
        enabled: true,
        path: "/s",
        auth: { type: "no_auth", scheme: "no_auth" },
        destinations: [{ destination: "shared", target: "t", create: true }],
      },
    },
  };
  const { nodes, edges } = fromConfig(config);
  expect(nodes).toHaveLength(2);
  const dest = nodes.find((n) => n.data.kind === "destination")!;
  const ep = nodes.find((n) => n.data.kind === "endpoint")!;
  expect(dest.id).not.toBe(ep.id);
  expect((dest.data as any).name).toBe("shared");
  expect((ep.data as any).name).toBe("shared");

  const destEdge = edges.find((e) => e.target === dest.id);
  expect(destEdge).toBeDefined();
});

test("duplicate routes to the same destination get distinct edge ids", () => {
  const config = {
    destinations: { d: { type: "rabbitmq", rabbitmq: { url: "u", exchange: "e", exchange_type: "direct" } } },
    endpoints: {
      a: {
        enabled: true,
        path: "/a",
        auth: { type: "no_auth", scheme: "no_auth" },
        destinations: [
          { destination: "d", target: "t1", create: true },
          { destination: "d", target: "t2", create: true },
        ],
      },
    },
  };
  const { edges } = fromConfig(config);
  const destEdges = edges.filter((e) => e.target === "destination:d");
  expect(destEdges).toHaveLength(2);
  expect(destEdges[0].id).not.toBe(destEdges[1].id);
});

test("throws when destinations is not a mapping", () => {
  expect(() => fromConfig({ destinations: "nope" })).toThrow("destinations must be a mapping");
});

test("throws when endpoints is not a mapping", () => {
  expect(() => fromConfig({ endpoints: ["nope"] })).toThrow("endpoints must be a mapping");
});

test("skips dangling edges when route destination has no matching destination node", () => {
  const config = {
    destinations: {},
    endpoints: {
      a: {
        enabled: true,
        path: "/a",
        auth: { type: "no_auth", scheme: "no_auth" },
        destinations: [{ destination: "missing", target: "t", create: true }],
      },
    },
  };
  const { nodes, edges } = fromConfig(config);
  expect(nodes).toHaveLength(1);
  expect(edges).toHaveLength(0);
});

test("importSettings recurses a nested block object into the nested settings shape, round-tripping with mapSettings", () => {
  const config = {
    destinations: {
      d: { type: "__group_test__", __group_test__: { name: "n", retry: { max_attempts: 3 } } },
    },
    endpoints: {},
  };
  const { nodes } = fromConfig(config);
  const dest = nodes.find((n) => n.data.kind === "destination")!;
  expect((dest.data as any).settings).toEqual({ name: "n", retry: { max_attempts: 3 } });

  const cfg2 = toConfig(nodes, []).config;
  expect((cfg2.destinations as any).d.__group_test__).toEqual({ name: "n", retry: { max_attempts: 3 } });
});

test("importSettings leaves a group field unset when the block value isn't a plain object", () => {
  const config = {
    destinations: { d: { type: "__group_test__", __group_test__: { name: "n", retry: "oops" } } },
    endpoints: {},
  };
  const { nodes } = fromConfig(config);
  const dest = nodes.find((n) => n.data.kind === "destination")!;
  expect((dest.data as any).settings).toEqual({ name: "n" });
});

// A2/B1: the full rabbitmq advanced surface (now MANAGED via the group specs)
// round-trips exactly, and a genuinely-unknown block key is still preserved
// verbatim via rawExtra.
test("destination round-trips the full rabbitmq block and preserves an unknown key", () => {
  const primary = {
    type: "rabbitmq",
    rabbitmq: {
      url: "${RABBITMQ_URL}",
      exchange: "hookway.webhooks",
      exchange_type: "direct",
      durable: true,
      publish_timeout: "5s",
      declare_queues: true,
      publisher_channels: 8,
      retry: { max_attempts: 3, initial_backoff: "100ms", max_backoff: "2s" },
      queue_defaults: { message_ttl: "24h", max_length: 100000, overflow: "reject-publish" },
      dead_letter: {
        enabled: true,
        exchange: "hookway.webhooks.dlx",
        exchange_type: "direct",
        queue: "hookway.webhooks.dlq",
        routing_key: "webhooks.dead",
        durable: true,
        create_queue: true,
      },
      x_future_broker_opt: "preserved", // key the UI doesn't model
    },
  };
  const config = { destinations: { primary }, endpoints: {} };
  const { nodes, edges } = fromConfig(config);
  const dest = nodes.find((n) => n.data.kind === "destination")!;
  // Only the unknown key lands in rawExtra now; the advanced blocks are managed.
  expect((dest.data as any).rawExtra.rabbitmq).toEqual({ x_future_broker_opt: "preserved" });
  const cfg2 = toConfig(nodes, edges).config;
  // The whole rabbitmq block round-trips (managed groups + preserved unknown).
  expect((cfg2.destinations as any).primary).toEqual(primary);
});

// A3: unmodeled endpoint + route keys (rate_limit, replay_protection,
// max_body_bytes, delivery_failure_policy, queue_options) survive round-trip,
// and enabled:false is preserved (no longer hardcoded true).
test("endpoint + route raw passthrough round-trips advanced keys and preserves enabled:false", () => {
  const config = {
    destinations: { primary: { type: "rabbitmq", rabbitmq: { url: "u", exchange: "e", exchange_type: "direct" } } },
    endpoints: {
      paddle: {
        enabled: false,
        path: "/webhooks/paddle",
        delivery_failure_policy: "stop_on_failure",
        max_body_bytes: 2097152,
        rate_limit: { enabled: true, requests_per_minute: 500, burst: 1000, key_by: "ip" },
        replay_protection: { enabled: true, ttl: "10m", max_entries: 50000 },
        auth: { type: "hmac_sha256", scheme: "hmac_sha256_ts_colon", secret: "${S}", signature_header: "Paddle-Signature" },
        destinations: [
          { destination: "primary", target: "paddle-webhooks", create: true, queue_options: { message_ttl: "72h", max_length: 500000, dead_letter_routing_key: "paddle.dead" } },
        ],
      },
    },
  };
  const { nodes, edges } = fromConfig(config);
  const ep = nodes.find((n) => n.data.kind === "endpoint")!;
  expect((ep.data as any).enabled).toBe(false);
  expect((ep.data as any).rawExtra).toEqual({
    delivery_failure_policy: "stop_on_failure",
    max_body_bytes: 2097152,
    rate_limit: config.endpoints.paddle.rate_limit,
    replay_protection: config.endpoints.paddle.replay_protection,
  });
  const routeEdge = edges.find((e) => e.target === "destination:primary")!;
  expect((routeEdge.data as any).rawExtra).toEqual({ queue_options: config.endpoints.paddle.destinations[0].queue_options });

  const cfg2 = toConfig(nodes, edges).config;
  expect((cfg2.endpoints as any).paddle).toEqual(config.endpoints.paddle);
});

// A4: the whole server block and every unmodeled top-level block round-trip.
test("top-level passthrough preserves server + security/metrics/logging/replay", () => {
  const config = {
    server: { addr: ":9090", max_body_bytes: 2048, read_timeout: "5s", max_inflight_requests: 256, fanout_concurrency: 8 },
    security: { api_key: "${API_KEY}", trusted_proxies: ["10.0.0.0/8"] },
    metrics: { enabled: true, path: "/metrics" },
    logging: { level: "debug" },
    replay: { enabled: true, addr: ":7070", ttl: "1h" },
    destinations: { primary: { type: "rabbitmq", rabbitmq: { url: "u", exchange: "e", exchange_type: "direct" } } },
    endpoints: { ep: { enabled: true, path: "/x", auth: { type: "no_auth" }, destinations: [{ destination: "primary", target: "t", create: true }] } },
  };
  const { nodes, edges, serverConfig, configExtra } = fromConfig(config);
  expect(serverConfig).toEqual(config.server);
  expect(configExtra).toEqual({ security: config.security, metrics: config.metrics, logging: config.logging, replay: config.replay });

  const cfg2 = toConfig(nodes, edges, serverConfig, configExtra).config;
  expect(cfg2.server).toEqual(config.server);
  expect(cfg2.security).toEqual(config.security);
  expect(cfg2.metrics).toEqual(config.metrics);
  expect(cfg2.logging).toEqual(config.logging);
  expect(cfg2.replay).toEqual(config.replay);
});

test("fresh graph (no server import) exports the default server block", () => {
  const cfg = toConfig([], []).config;
  expect(cfg.server).toEqual({ addr: ":8080", max_body_bytes: 1048576 });
});

test("destination raw passthrough preserves an entry-level key (on_existing_conflict)", () => {
  const d = { type: "sqs", on_existing_conflict: "reconcile", sqs: { region: "us-east-1" } };
  const config = { destinations: { d }, endpoints: {} };
  const { nodes, edges } = fromConfig(config);
  const dest = nodes.find((n) => n.data.kind === "destination")!;
  expect((dest.data as any).rawExtra).toEqual({ on_existing_conflict: "reconcile" });
  const cfg2 = toConfig(nodes, edges).config;
  expect((cfg2.destinations as any).d).toEqual(d);
});
