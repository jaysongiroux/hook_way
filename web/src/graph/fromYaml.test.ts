import { expect, test } from "vitest";
import { fromYaml } from "./fromYaml";

test("parses YAML into positioned nodes + edges", () => {
  const yaml = `
server: { addr: ":8080", max_body_bytes: 1048576 }
destinations:
  primary: { type: rabbitmq, rabbitmq: { url: u, exchange: e, exchange_type: direct } }
endpoints:
  github:
    enabled: true
    path: /webhooks/github
    auth: { type: hmac_sha256, scheme: hmac_sha256_hex, signature_header: X, secret: "\${S}" }
    destinations: [{ destination: primary, target: gh, create: true }]
`;
  const { nodes, edges } = fromYaml(yaml);
  expect(nodes.map((n) => n.id).sort()).toEqual(["destination:primary", "endpoint:github"]);
  expect(edges).toHaveLength(1);
  // positions assigned by dagre (not all zero)
  expect(nodes.some((n) => n.position.x !== 0 || n.position.y !== 0)).toBe(true);
});
