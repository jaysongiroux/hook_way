import { expect, test } from "vitest";
import { toYaml } from "./toYaml";

test("renders config as YAML with flow notes as comments", () => {
  const config = {
    schema_version: 2,
    server: { addr: ":8080", max_body_bytes: 1048576 },
    flows: { github: { nodes: [{ id: "in", type: "http_trigger", path: "/webhooks/github" }] } },
    destinations: { primary: { type: "webhook" } },
  };
  const yaml = toYaml(config, { flows: { github: "GitHub events" }, destinations: {} });
  expect(yaml).toContain("# GitHub events");
  expect(yaml).toContain("path: /webhooks/github");
  expect(yaml).toContain("max_body_bytes: 1048576");
});
