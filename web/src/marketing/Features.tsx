import { Anchor, Box, Tabs, Text, Title } from "@mantine/core";
import { useLayoutEffect, useState } from "react";
import { useReducedMotion } from "@mantine/hooks";
import { useAnimate } from "motion/react-mini";
import { Link } from "react-router-dom";
import { Section } from "./Section";
import { DARK } from "../tokens";
import { DEST_TYPES, NODE_SCHEMAS } from "../graph/nodeSchema";

const FEATURES: { title: string; body: string }[] = [
  {
    title: "Send events to the systems you run",
    body: `Publish to ${DEST_TYPES.length} destination types: queues, streams, databases, S3, Lambda and HTTP webhooks. Reuse credentials with named connections.`,
  },
  {
    title: "Expose APIs as MCP tools",
    body: "Import an OpenAPI spec, select operations, and review the generated tools. Give each caller explicit permissions with API keys or OAuth.",
  },
  {
    title: "Verify who sent each request",
    body: `Choose from ${Object.keys(NODE_SCHEMAS.auth.auth_schemes ?? {}).length} authentication formats: HMAC or ECDSA signatures, HTTP Basic, exact tokens, and JWT. Invalid credentials and unknown schemes are rejected; accepting unauthenticated traffic is an explicit choice.`,
  },
  {
    title: "Keep traffic protection consistent as you scale",
    body: "Control bursts, rolling quotas and duplicates by caller or verified tenant. Use memory for one server; Redis or PostgreSQL across servers. Reject affected requests if protection becomes unavailable.",
  },
  {
    title: "Handle events and file uploads",
    body: "Read JSON, XML, multipart form data, or raw bodies. Use fields to route events, store decoded file bytes in S3 or Redis key/value storage, or forward the original request to an API.",
  },
  {
    title: "Handle synchronous HTTP requests",
    body: "Proxy to an API or run a native handler. Check and transform requests, then return your configured response or the upstream status and body on the same connection.",
  },
  {
    title: "Decide which events go where",
    body: "Filter unwanted events. Use if / else for two outcomes, or a switch for several routes, based on headers and parsed body fields.",
  },
  {
    title: "Prepare data for the next service",
    body: "Check required JSON fields, value types and formats before delivery. Map or remove fields, compare JSON changes, or clean up ordered XML. Use sandboxed JavaScript when the receiving service needs a custom payload.",
  },
  {
    title: "Route to the right environment",
    body: "Resolve allowed environments with HTTP, PostgreSQL or DynamoDB lookups. Save a route after resource creation so later events reach the same backend.",
  },
  {
    title: "Process batches with limits",
    body: "Split a JSON batch, check each event’s environment, and run its flow. Bound the work with batch size, concurrency, and time limits.",
  },
  {
    title: "Investigate uncertain deliveries",
    body: "Retry only a failed destination through the optional replay console or API. Investigate uncertain delivery before repeating it, or record independent confirmation with a note, preserving the evidence.",
  },
];

export function Features() {
  const [active, setActive] = useState<string | null>("connect");
  const [scope, animate] = useAnimate<HTMLDivElement>();
  const reducedMotion = useReducedMotion(undefined, {
    getInitialValueInEffect: false,
  });
  useLayoutEffect(() => {
    if (reducedMotion || typeof Element.prototype.animate !== "function")
      return;
    const animation = animate(
      `[data-feature-panel="${active}"]`,
      { opacity: [0, 1], transform: ["translateY(8px)", "translateY(0px)"] },
      { duration: 0.28, ease: [0.22, 1, 0.36, 1] },
    );
    return () => animation.cancel();
  }, [active, reducedMotion, animate]);
  return (
    <Section
      title="Webhooks, HTTP handlers and MCP tools in the same builder"
      lead="Choose how requests are verified, transformed and routed. Build visually or configure the same flows in YAML."
    >
      <Tabs
        value={active}
        onChange={setActive}
        ref={scope}
        className="hookway-feature-tabs"
      >
        <Tabs.List aria-label="Explore builder capabilities" grow>
          <Tabs.Tab value="connect">Connect systems</Tabs.Tab>
          <Tabs.Tab value="prepare">Check &amp; prepare</Tabs.Tab>
          <Tabs.Tab value="route">Route &amp; recover</Tabs.Tab>
        </Tabs.List>
        {[
          { id: "connect", features: [0, 5, 1] },
          { id: "prepare", features: [2, 3, 4, 7] },
          { id: "route", features: [6, 8, 9, 10] },
        ].map((group) => (
          <Tabs.Panel
            key={group.id}
            value={group.id}
            data-feature-panel={group.id}
          >
            {group.features.map((index) => (
              <Box
                key={FEATURES[index].title}
                className="hookway-capability-row"
              >
                <Title order={3} size="h5" c={DARK.text}>
                  {FEATURES[index].title}
                </Title>
                <Text c={DARK.muted} size="sm">
                  {FEATURES[index].body}
                </Text>
              </Box>
            ))}
          </Tabs.Panel>
        ))}
      </Tabs>
      <Text size="sm" c={DARK.muted} mt={24}>
        Create a flow, import existing YAML, or export a configuration. No
        sign-in required.{" "}
        <Anchor component={Link} to="/builder">
          Open the public builder
        </Anchor>
      </Text>
    </Section>
  );
}
