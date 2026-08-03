import { expect, test } from "vitest";
import { toYaml } from "./toYaml";

test("renders config as YAML with endpoint notes as comments", () => {
  const config = {
    server: { addr: ":8080", max_body_bytes: 1048576 },
    endpoints: { github: { path: "/webhooks/github" } },
    destinations: { primary: { type: "rabbitmq" } },
  };
  const yaml = toYaml(config, { endpoints: { github: "GitHub events" }, destinations: {} });
  expect(yaml).toContain("# GitHub events");
  expect(yaml).toContain("path: /webhooks/github");
  expect(yaml).toContain("max_body_bytes: 1048576");
});
