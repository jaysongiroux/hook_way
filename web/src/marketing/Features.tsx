import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { Section } from "./Section";
import { DARK } from "../tokens";
import { DEST_TYPES, NODE_SCHEMAS } from "../graph/nodeSchema";

const FEATURES: { span: 2 | 3 | 4 | 6; title: string; body: string }[] = [
  {
    span: 3,
    title: "Send events to the systems you run",
    body: `Connect to ${DEST_TYPES.length} destination types, including RabbitMQ, SQS, Kafka, Redis, databases, S3, Lambda and HTTP webhooks. Reuse credentials across destinations with named connections.`,
  },
  {
    span: 3,
    title: "Verify who sent each request",
    body: `Choose from ${Object.keys(NODE_SCHEMAS.auth.auth_schemes ?? {}).length} authentication formats: HMAC or ECDSA signatures, HTTP Basic, exact tokens, and JWT. Invalid credentials and unknown schemes are rejected; accepting unauthenticated traffic is an explicit choice.`,
  },
  {
    span: 3,
    title: "Keep traffic protection consistent as you scale",
    body: "Share rate limits and duplicate checks across servers with Redis or PostgreSQL, or use memory for one server. Control bursts or rolling quotas by caller or verified tenant. If protection becomes unavailable, affected requests are rejected until it recovers.",
  },
  {
    span: 3,
    title: "Handle events and file uploads",
    body: "Read JSON, XML, multipart form data, or raw bodies. Use fields to route events, store decoded file bytes in S3 or Redis key/value storage, or forward the original request to an API.",
  },
  {
    span: 2,
    title: "Decide which events go where",
    body: "Filter out events you do not need. Use if / else for two outcomes, or a switch for several routes. Conditions can check request headers and fields in the parsed body.",
  },
  {
    span: 2,
    title: "Prepare data for the next service",
    body: "Check required JSON fields, value types and formats before delivery. Map or remove fields, compare JSON changes, or clean up ordered XML. Use sandboxed JavaScript when the receiving service needs a custom payload.",
  },
  {
    span: 2,
    title: "Route to the right environment",
    body: "Choose only environments allowed by your routing rules. Use HTTP, PostgreSQL or DynamoDB lookups to resolve routing values. Save a new route after an API creates a resource so later events reach the same backend.",
  },
  {
    span: 2,
    title: "Call APIs and return their responses",
    body: "Forward a request to an HTTP API and return its response to the caller. Choose which headers, query parameters, and body to send; managed credential profiles handle outbound authentication.",
  },
  {
    span: 2,
    title: "Process batches with limits",
    body: "Split a JSON batch into individual events, check each event’s environment, and deliver each through its own flow. Set batch size, concurrency, and time limits to bound the work.",
  },
  {
    span: 2,
    title: "Investigate uncertain deliveries",
    body: "Retry only a failed destination through the optional replay console or API. Investigate uncertain delivery before repeating it, or record independent confirmation with a note, preserving the evidence.",
  },
];

export function Features() {
  return (
    <Section
      title="From incoming webhook to delivered event"
      lead="Define how requests are checked, how their data is prepared, and where each event goes. Configure the flow in YAML or export it from the visual builder."
    >
      <Bento>
        {FEATURES.map((f) => (
          <Tile key={f.title} span={f.span}>
            <Stack gap={8}>
              <Text fw={600} c={DARK.text} size="md">
                {f.title}
              </Text>
              <Text size="sm" c={DARK.muted}>
                {f.body}
              </Text>
            </Stack>
          </Tile>
        ))}
      </Bento>
    </Section>
  );
}
