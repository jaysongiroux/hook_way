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
    expect(post.data.options.forward_headers, id).toBeUndefined();
    expect(post.data.options.forward_method, id).toBeFalsy();
    expect(post.data.options.forward_query, id).toBeFalsy();
    const statuses = post.data.options.error_on_status as string[] | undefined;
    expect(statuses, id).toContain("4xx");
    const retry = post.data.options.retry as Record<string, unknown> | undefined;
    expect(retry, id).toBeDefined();
    expect(Number(retry!.max_attempts), id).toBeGreaterThan(1);
  }
});

test("the billing ingest templates verify the provider signature layout", () => {
  const stripe = templateSubgraph("stripe-ingest")!;
  const stripeAuth = stripe.nodes.find((n) => n.data.nodeType === "auth")!;
  expect(stripeAuth.data.options.scheme).toBe("hmac_sha256_ts_dot");
  expect(stripeAuth.data.options.signature_header).toBe("Stripe-Signature");
  expect(stripeAuth.data.options.secret).toBeUndefined();
  expect(stripeAuth.data.options.secret_ref).toEqual({
    provider: "env",
    name: "STRIPE_WEBHOOK_SECRET",
    refresh: "15m",
    overlap: "5m",
  });
  expect(stripeAuth.data.options.timestamp_tolerance).toBe("5m");
  expect(stripe.nodes.map((n) => n.data.nodeType)).toEqual([
    "http_trigger",
    "loop_guard",
    "rate_limit",
    "read_body",
    "auth",
    "replay_guard",
    "envelope",
  ]);
  const stripeLimit = stripe.nodes.find((n) => n.data.nodeType === "rate_limit")!;
  expect(stripeLimit.data.options.key_by).toBe("source_ip");
  expect(stripe.nodes.some((n) => n.data.nodeType === "publish")).toBe(false);

  const paddle = templateSubgraph("paddle-ingest")!;
  const paddleAuth = paddle.nodes.find((n) => n.data.nodeType === "auth")!;
  expect(paddleAuth.data.options.scheme).toBe("hmac_sha256_ts_colon");
  expect(paddleAuth.data.options.signature_header).toBe("Paddle-Signature");
  expect(paddleAuth.data.options.secret).toBeUndefined();
  expect(paddleAuth.data.options.secret_ref).toEqual({
    provider: "env",
    name: "PADDLE_WEBHOOK_SECRET",
    refresh: "15m",
    overlap: "5m",
  });
  expect(paddleAuth.data.options.timestamp_tolerance).toBe("5m");
  expect(paddle.nodes.map((n) => n.data.nodeType)).toEqual([
    "http_trigger",
    "loop_guard",
    "rate_limit",
    "read_body",
    "auth",
    "replay_guard",
    "envelope",
  ]);
  expect(paddle.nodes.some((n) => n.data.nodeType === "publish")).toBe(false);
});

test("the xml template keeps the envelope, filter, prune and forward consistent", () => {
  const xml = templateSubgraph("xml-ingest")!;
  // The whole pipeline travels together: xml_prune only validates behind an
  // xml envelope, so a front/back split would insert an invalid config.
  expect(xml.nodes.map((n) => n.data.nodeType)).toEqual([
    "http_trigger",
    "loop_guard",
    "rate_limit",
    "read_body",
    "auth",
    "replay_guard",
    "envelope",
    "filter",
    "code",
    "transform",
    "http_request",
  ]);
  expect(xml.nodes.find((n) => n.data.nodeType === "envelope")!.data.options.body_format).toBe("xml");

  const prune = xml.nodes.find((n) => n.data.nodeType === "transform")!;
  expect(prune.data.options.kind).toBe("xml_prune");
  expect((prune.data.options.rules as string[]).length).toBeGreaterThan(0);

  // The filter teaches the XML path syntax, so it must read the body rather
  // than a header.
  const conditions = xml.nodes.find((n) => n.data.nodeType === "filter")!.data.options
    .conditions as Array<Record<string, unknown>>;
  expect(conditions[0].source).toBe("body");
  expect(String(conditions[0].path)).toContain(".");

  // The script must return the body it was given, or the prune below it has
  // nothing to walk.
  const script = String(xml.nodes.find((n) => n.data.nodeType === "code")!.data.options.script);
  expect(script).toContain("input.body");
  expect(script).toContain("children");

  const post = xml.nodes.find((n) => n.data.nodeType === "http_request")!;
  expect(post.data.options.body).toBe("transformed");
  expect(post.data.options.url).toBe("${XML_UPSTREAM_URL}");
  expect(post.data.options.forward_headers).toBeUndefined();
});

test("every template config authenticates inbound traffic and keeps secrets out of YAML", () => {
  for (const def of TEMPLATES) {
    expect(def.yaml, def.id).not.toMatch(/\btype:\s*no_auth\b/);
    expect(def.yaml, def.id).not.toMatch(/insecure_skip_verify:\s*true/);
    expect(def.yaml, def.id).toContain("secret_ref:");
    expect(def.yaml, def.id).toContain("type: loop_guard");
    expect(def.yaml, def.id).toContain("type: rate_limit");
    expect(def.yaml, def.id).toContain("type: replay_guard");
    expect(def.yaml, def.id).toContain("forwarded_header_allowlist:");
    expect(def.yaml, def.id).toContain("api_key: \"${HOOKWAY_API_KEY}\"");
    expect(def.yaml, def.id).not.toMatch(/secret:\s*["'][^$"']/);
  }
});
