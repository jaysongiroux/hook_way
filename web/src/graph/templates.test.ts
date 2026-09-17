import { expect, test } from "vitest";
import { isValidConnection } from "./connection";
import { TEMPLATES, templateSubgraph, isTemplateId } from "./templates";

test("every manifest entry extracts the node ids it names", () => {
  expect(TEMPLATES.length).toBeGreaterThan(0);
  for (const def of TEMPLATES) {
    const sub = templateSubgraph(def.id);
    expect(sub, def.id).toBeDefined();
    const ids = sub!.nodes.map((n) => n.id).sort();
    expect(ids, def.id).toEqual([...def.nodeIds].sort());
  }
});

test("every manifest entry has a label and a teaching sentence", () => {
  for (const def of TEMPLATES) {
    expect(def.label.length, def.id).toBeGreaterThan(3);
    expect(def.help.length, def.id).toBeGreaterThan(20);
  }
});

test("template ids are unique and recognized", () => {
  const ids = TEMPLATES.map((t) => t.id);
  expect(new Set(ids).size).toBe(ids.length);
  for (const id of ids) expect(isTemplateId(id)).toBe(true);
  expect(isTemplateId("not-a-template")).toBe(false);
  expect(templateSubgraph("not-a-template")).toBeUndefined();
});

test("extracted edges stay inside the extracted node set", () => {
  for (const def of TEMPLATES) {
    const sub = templateSubgraph(def.id)!;
    const ids = new Set(sub.nodes.map((n) => n.id));
    for (const edge of sub.edges) {
      expect(ids.has(edge.source), `${def.id}: ${edge.source}`).toBe(true);
      expect(ids.has(edge.target), `${def.id}: ${edge.target}`).toBe(true);
    }
    expect(sub.edges.length, def.id).toBeGreaterThan(0);
  }
});

test("every extracted edge is a legal connection", () => {
  for (const def of TEMPLATES) {
    const sub = templateSubgraph(def.id)!;
    sub.edges.forEach((edge, i) => {
      const ok = isValidConnection(
        {
          source: edge.source,
          target: edge.target,
          sourceHandle: edge.sourceHandle ?? "out",
          targetHandle: edge.targetHandle ?? "in",
        },
        sub.nodes,
        sub.edges.slice(0, i),
      );
      expect(ok, `${def.id}: ${edge.source}>${edge.target}`).toBe(true);
    });
  }
});

test("the chat templates carry the platform payload shape and its secret URL", () => {
  const slack = templateSubgraph("slack-notify")!;
  const slackScript = String(slack.nodes.find((n) => n.data.nodeType === "code")!.data.options.script);
  expect(slackScript).toContain("text");
  const slackURL = String(slack.nodes.find((n) => n.data.nodeType === "http_request")!.data.options.url);
  expect(slackURL).toBe("${SLACK_WEBHOOK_URL}");

  const discord = templateSubgraph("discord-notify")!;
  const discordScript = String(discord.nodes.find((n) => n.data.nodeType === "code")!.data.options.script);
  expect(discordScript).toContain("content");
  const discordURL = String(discord.nodes.find((n) => n.data.nodeType === "http_request")!.data.options.url);
  expect(discordURL).toBe("${DISCORD_WEBHOOK_URL}");
});

test("the chat templates post with the transformed body and retry transient failures", () => {
  for (const id of ["slack-notify", "discord-notify"]) {
    const post = templateSubgraph(id)!.nodes.find((n) => n.data.nodeType === "http_request")!;
    expect(post.data.options.body, id).toBe("transformed");
    const retry = post.data.options.retry as Record<string, unknown> | undefined;
    expect(retry, id).toBeDefined();
    expect(Number(retry!.max_attempts), id).toBeGreaterThan(1);
  }
});
