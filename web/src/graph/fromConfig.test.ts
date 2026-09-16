import { expect, test } from "vitest";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { createNode, resetIdCounters } from "./model";

test("imports a flow into canvas nodes and edges", () => {
  const config = {
    server: { addr: ":8080", max_body_bytes: 1048576 },
    destinations: { primary: { type: "webhook", webhook: { url: "https://example.test" } } },
    flows: {
      github: {
        nodes: [
          { id: "in", type: "http_trigger", path: "/webhooks/github", methods: ["POST"] },
          { id: "out", type: "publish", destination: "primary", target: "github-webhooks" },
        ],
        edges: ["in>out"],
      },
    },
  };
  const doc = fromConfig(config);
  expect(doc.activeFlow).toBe("github");
  expect(doc.nodes.map((n) => n.id)).toEqual(["in", "out"]);
  expect(doc.nodes[0].data.nodeType).toBe("http_trigger");
  expect(doc.edges).toHaveLength(1);
  expect(doc.destinations).toEqual(config.destinations);
});

test("round-trips a built flow through toConfig/fromConfig", () => {
  resetIdCounters();
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  const pub = createNode("publish", { x: 0, y: 0 });
  pub.data.options = { destination: "primary", target: "t" };
  const doc = fromConfig(
    toConfig({
      nodes: [trigger, pub],
      edges: [{ id: "e1", source: trigger.id, target: pub.id, sourceHandle: "out", targetHandle: "in" }],
      serverConfig: { addr: ":8080", max_body_bytes: 1048576 },
      configExtra: {},
      destinations: { primary: { type: "webhook", webhook: { url: "https://x" } } },
      activeFlow: "main",
      otherFlows: {},
    }).config,
  );
  expect(doc.nodes.map((n) => n.data.nodeType).sort()).toEqual(["http_trigger", "publish"]);
  expect(doc.edges).toHaveLength(1);
});

test("description is node identity, not an option", () => {
  const doc = fromConfig({
    flows: {
      github: {
        nodes: [{ id: "auth", type: "auth", description: "Verify the GitHub signature.", scheme: "hmac_sha256_hex" }],
        edges: [],
      },
    },
  });
  // The options form is driven by the generated node schema, which has no
  // description property. Leaving it in options would render a stray field
  // and export it into the same place, so it must be lifted out.
  expect(doc.nodes[0].data.description).toBe("Verify the GitHub signature.");
  expect(doc.nodes[0].data.options).toEqual({ scheme: "hmac_sha256_hex" });
});

test("description survives a toConfig/fromConfig round trip", () => {
  resetIdCounters();
  const node = createNode("http_trigger", { x: 0, y: 0 });
  node.data.description = "Receives GitHub webhook deliveries.";
  const { config } = toConfig({
    nodes: [node],
    edges: [],
    serverConfig: { addr: ":8080" },
    configExtra: {},
    destinations: {},
    activeFlow: "main",
    otherFlows: {},
  });
  const emitted = (config.flows as Record<string, { nodes: Record<string, unknown>[] }>).main.nodes[0];
  expect(Object.keys(emitted).slice(0, 3)).toEqual(["id", "type", "description"]);
  expect(fromConfig(config).nodes[0].data.description).toBe("Receives GitHub webhook deliveries.");
});

test("a blank description is omitted rather than exported as an empty key", () => {
  resetIdCounters();
  const node = createNode("http_trigger", { x: 0, y: 0 });
  node.data.description = "   ";
  const { config } = toConfig({
    nodes: [node],
    edges: [],
    serverConfig: { addr: ":8080" },
    configExtra: {},
    destinations: {},
    activeFlow: "main",
    otherFlows: {},
  });
  const emitted = (config.flows as Record<string, { nodes: Record<string, unknown>[] }>).main.nodes[0];
  expect(emitted).not.toHaveProperty("description");
});
