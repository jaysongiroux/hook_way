import { expect, test } from "vitest";
import { toYaml } from "./toYaml";

test("renders config as YAML", () => {
  const config = {
    server: { addr: ":8080", max_body_bytes: 1048576 },
    flows: {
      github: {
        nodes: [{ id: "in", type: "http_trigger", description: "GitHub events", path: "/webhooks/github" }],
      },
    },
    destinations: { primary: { type: "webhook" } },
  };
  const yaml = toYaml(config);
  expect(yaml).toContain("description: GitHub events");
  expect(yaml).toContain("path: /webhooks/github");
  expect(yaml).toContain("max_body_bytes: 1048576");
});
