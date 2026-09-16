import { expect, test } from "vitest";
import { fromYaml } from "./fromYaml";

test("parses flow YAML into positioned nodes + edges", () => {
  const yaml = `
schema_version: 2
server: { addr: ":8080", max_body_bytes: 1048576 }
destinations:
  primary: { type: webhook, webhook: { url: https://example.test } }
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: out, type: publish, destination: primary, target: gh}
    edges: [in>out]
`;
  const { nodes, edges, activeFlow } = fromYaml(yaml);
  expect(activeFlow).toBe("github");
  expect(nodes.map((n) => n.id).sort()).toEqual(["in", "out"]);
  expect(edges).toHaveLength(1);
  expect(nodes.some((n) => n.position.x !== 0 || n.position.y !== 0)).toBe(true);
});

test("layouts inactive flows so they are not stacked at the origin", () => {
  const yaml = `
schema_version: 2
server: { addr: ":8080" }
destinations:
  primary: { type: webhook, webhook: { url: https://example.test } }
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /github, methods: [POST]}
      - {id: out, type: publish, destination: primary, target: gh}
    edges: [in>out]
  billing:
    nodes:
      - {id: in, type: http_trigger, path: /billing, methods: [POST]}
      - {id: out, type: publish, destination: primary, target: bill}
    edges: [in>out]
`;
  const doc = fromYaml(yaml);
  const billing = doc.otherFlows.billing.nodes;
  expect(billing).toHaveLength(2);
  expect(billing[0].position).not.toEqual(billing[1].position);
});
