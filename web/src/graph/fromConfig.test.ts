import { beforeEach, expect, test } from "vitest";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { createNode, resetIdCounters } from "./model";
import type { Edge } from "@xyflow/react";

beforeEach(() => resetIdCounters());

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
